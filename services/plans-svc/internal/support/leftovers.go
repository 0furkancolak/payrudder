package support

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// The parts of the help desk that came after the first desk (plan 12, round 4): the plan and days off behind the first-answer deadline, ratings, merging two tickets
// of one person, and links from a ticket to the objects it is about. Defaults here are not agreed; the plan file lists them.

func mergedErr() error {
	return errkey.New(codes.FailedPrecondition, "support.ticket.merged", "this ticket was merged into another; write there")
}

// offDays reads the days off of the app whose calendar a ticket's clock uses (nil app: none).
func offDays(ctx context.Context, q *sqlc.Queries, app *uuid.UUID) (map[string]bool, error) {
	if app == nil {
		return nil, nil
	}
	rows, err := q.ListSupportHolidays(ctx, *app)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.Day] = true
	}
	return out, nil
}

// clockApp is the app a ticket's clock counts days off by; the query answers the nil uuid for none.
func clockApp(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// --- links

// linksOf reads the links of a ticket as the API shows them.
func linksOf(ctx context.Context, q *sqlc.Queries, ticketID uuid.UUID) ([]*plansv1.SupportLink, error) {
	rows, err := q.ListSupportLinks(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	out := make([]*plansv1.SupportLink, len(rows))
	for i, r := range rows {
		out[i] = &plansv1.SupportLink{Kind: r.Kind, Ref: r.Ref, AppId: r.AppID.String(), Environment: r.Environment, TenantId: r.LinkTenantID.String(), AppName: r.AppName}
	}
	return out, nil
}

// setLinks makes the ticket's links exactly these: at most five, each a customer, invoice or subscription of an app of the ticket's account, each once. The tenant of an
// app environment is looked up under the account, so a person cannot point at another account's app.
func setLinks(ctx context.Context, q *sqlc.Queries, ticketID, account uuid.UUID, links []*plansv1.SupportLink) error {
	if len(links) > domain.SupportMaxLinks {
		return domain.Invalid("links", fmt.Sprintf("at most %d", domain.SupportMaxLinks))
	}
	type target struct {
		tenant    uuid.UUID
		kind, ref string
	}
	var want []target
	seen := map[target]bool{}
	for _, l := range links {
		kind, ref, env, err := domain.NormalizeSupportLink(l.GetKind(), l.GetRef(), l.GetEnvironment())
		if err != nil {
			return err
		}
		app, err := uuid.Parse(l.GetAppId())
		if err != nil {
			return errkey.New(codes.NotFound, "support.app.not_found", "app not found")
		}
		tenantID, err := q.SupportLinkTenant(ctx, sqlc.SupportLinkTenantParams{AccountID: account, AppID: app, Environment: env})
		if errors.Is(err, pgx.ErrNoRows) {
			return errkey.New(codes.NotFound, "support.app.not_found", "app not found")
		} else if err != nil {
			return err
		}
		t := target{tenantID, kind, ref}
		if !seen[t] {
			seen[t] = true
			want = append(want, t)
		}
	}
	if err := q.DeleteSupportLinks(ctx, ticketID); err != nil {
		return err
	}
	for _, t := range want {
		if err := q.InsertSupportLink(ctx, sqlc.InsertSupportLinkParams{ID: idgen.NewUUIDv7(), TicketID: ticketID, LinkTenantID: t.tenant, Kind: t.kind, Ref: t.ref}); err != nil {
			return err
		}
	}
	return nil
}

// --- ratings

// RateMySupportTicket records what the person thought of a ticket that is solved or closed. A second call replaces the first; a ticket not solved yet takes none.
func (s *Service) RateMySupportTicket(ctx context.Context, req *plansv1.RateMySupportTicketRequest) (*plansv1.RateMySupportTicketResponse, error) {
	user, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, notFound()
	}
	id, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	comment := strings.TrimSpace(req.GetComment())
	if err := domain.ValidateSupportRating(int(req.GetRating()), comment); err != nil {
		return nil, err
	}
	var out *plansv1.SupportTicket
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := lockedFor(ctx, q, id, &user)
		if err != nil {
			return err
		}
		if row.Status != "solved" && row.Status != "closed" {
			return errkey.New(codes.FailedPrecondition, "support.ticket.not_solved", "a ticket can be rated once it is solved")
		}
		if err := q.RateSupportTicket(ctx, sqlc.RateSupportTicketParams{ID: id, Rating: int16(req.GetRating()), Comment: comment}); err != nil { //nolint:gosec // 1 to 5
			return err
		}
		out, err = ticket(ctx, q, id, &user)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.RateMySupportTicketResponse{Ticket: out}, nil
}

// --- merge

// MergeSupportTickets moves the messages and files of one ticket to another ticket of the same person and account, and closes the first with a pointer to the other. It
// is the operator's call. The two are locked in the order of their ids, so two merges that name the same pair cannot wait for each other.
func (s *Service) MergeSupportTickets(ctx context.Context, req *plansv1.MergeSupportTicketsRequest) (*plansv1.MergeSupportTicketsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	from, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	into, err := parse(req.GetIntoTicketId())
	if err != nil {
		return nil, err
	}
	by := strings.TrimSpace(req.GetAuthorName())
	if by == "" {
		by = "Payrudder support"
	}
	var outFrom, outInto *plansv1.SupportTicket
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		first, second := from, into
		if second.String() < first.String() {
			first, second = second, first
		}
		rows := map[uuid.UUID]sqlc.LockSupportTicketRow{}
		for _, id := range []uuid.UUID{first, second} {
			r, err := lockedFor(ctx, q, id, nil)
			if err != nil {
				return err
			}
			rows[id] = r
		}
		a, b := rows[from], rows[into]
		side := func(r sqlc.LockSupportTicketRow) domain.SupportMergeSide {
			return domain.SupportMergeSide{ID: r.ID.String(), AccountID: r.AccountID.String(), OpenedBy: r.OpenedBy.String(), Status: r.Status, Merged: r.MergedInto != nil}
		}
		if why := domain.SupportMergeCheck(side(a), side(b)); why != "" {
			return errkey.New(codes.FailedPrecondition, "support.ticket.cannot_merge", why)
		}
		fromNumber, err := q.SupportTicketNumber(ctx, from)
		if err != nil {
			return err
		}
		intoNumber, err := q.SupportTicketNumber(ctx, into)
		if err != nil {
			return err
		}
		if err := q.MoveSupportMessages(ctx, sqlc.MoveSupportMessagesParams{FromID: from, IntoID: into}); err != nil {
			return err
		}
		if err := q.MoveSupportAttachments(ctx, sqlc.MoveSupportAttachmentsParams{FromID: from, IntoID: into}); err != nil {
			return err
		}
		// The ticket that keeps the thread: what the moved messages wait for is waiting there now (a person's message waiting for the operator reopens a ticket that was
		// pending or solved), and the earlier first-answer deadline of the two holds while neither was answered.
		prevInto, nextInto := stateOf(b), stateOf(b)
		if a.Status == "open" && (b.Status == "pending" || b.Status == "solved") {
			nextInto.Status, nextInto.SolvedAt = "open", nil
		}
		if nextInto.FirstRespondedAt == nil && a.FirstRespondedAt == nil && a.FirstResponseDue.Before(nextInto.FirstResponseDue) {
			nextInto.FirstResponseDue = a.FirstResponseDue
		}
		lastAuthor := lastAuthorOf(ctx, q, into)
		if nextInto.Status != prevInto.Status {
			lastAuthor = "customer"
		}
		if err := save(ctx, q, into, prevInto, nextInto, lastAuthor, true, actor{"operator", by}); err != nil {
			return err
		}
		prevFrom := stateOf(a)
		closed, err := domain.SupportSetStatus(prevFrom, "closed", true, s.now())
		if err != nil {
			return err
		}
		if err := save(ctx, q, from, prevFrom, closed, lastAuthorOf(ctx, q, from), false, actor{"operator", by}); err != nil {
			return err
		}
		if err := q.SetSupportTicketMerged(ctx, sqlc.SetSupportTicketMergedParams{ID: from, IntoID: &into}); err != nil {
			return err
		}
		// The history says it on both: the ticket that kept the thread names the one that joined it, and the one that joined names the one it joined.
		for _, e := range []struct {
			ticket   uuid.UUID
			from, to string
		}{{into, fmt.Sprintf("#%d", fromNumber), ""}, {from, "", fmt.Sprintf("#%d", intoNumber)}} {
			if err := q.InsertSupportEvent(ctx, sqlc.InsertSupportEventParams{ID: idgen.NewUUIDv7(), TicketID: e.ticket, Kind: "merge", ActorKind: "operator", ActorName: by, FromValue: e.from, ToValue: e.to}); err != nil {
				return err
			}
		}
		// Both are news to the person who opened them.
		for _, id := range []uuid.UUID{from, into} {
			if err := q.MarkSupportTicketUnseen(ctx, id); err != nil {
				return err
			}
		}
		if err := audit.Record(ctx, tx, audit.Event{AccountID: a.AccountID, Action: "support.merged", TargetType: "support_ticket", TargetID: from.String(),
			Summary: fmt.Sprintf("support merged ticket #%d into #%d", fromNumber, intoNumber), Detail: map[string]any{"from": from.String(), "into": into.String(), "by": by}}); err != nil {
			return err
		}
		if outFrom, err = ticket(ctx, q, from, nil); err != nil {
			return err
		}
		outInto, err = ticket(ctx, q, into, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.MergeSupportTicketsResponse{Ticket: outFrom, IntoTicket: outInto}, nil
}

// --- the days off of an app

// GetSupportHolidays lists the days off of an app.
func (s *Service) GetSupportHolidays(ctx context.Context, req *plansv1.GetSupportHolidaysRequest) (*plansv1.GetSupportHolidaysResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	app, err := uuid.Parse(req.GetAppId())
	if err != nil {
		return nil, errkey.New(codes.NotFound, "support.app.not_found", "app not found")
	}
	q := sqlc.New(s.pool)
	name, err := q.SupportAppName(ctx, app)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.NotFound, "support.app.not_found", "app not found")
	} else if err != nil {
		return nil, err
	}
	rows, err := q.ListSupportHolidays(ctx, app)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetSupportHolidaysResponse{AppName: name, Holidays: holidaysOf(rows)}, nil
}

func holidaysOf(rows []sqlc.ListSupportHolidaysRow) []*plansv1.SupportHoliday {
	out := make([]*plansv1.SupportHoliday, len(rows))
	for i, r := range rows {
		out[i] = &plansv1.SupportHoliday{Day: r.Day, Name: r.Name}
	}
	return out
}

// SetSupportHolidays replaces the days off of an app and records who did it. A ticket already open keeps its deadline; a change of its priority counts it again with the
// days as they are then.
func (s *Service) SetSupportHolidays(ctx context.Context, req *plansv1.SetSupportHolidaysRequest) (*plansv1.SetSupportHolidaysResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	app, err := uuid.Parse(req.GetAppId())
	if err != nil {
		return nil, errkey.New(codes.NotFound, "support.app.not_found", "app not found")
	}
	in := make([]domain.SupportHoliday, len(req.GetHolidays()))
	for i, h := range req.GetHolidays() {
		in[i] = domain.SupportHoliday{Day: h.GetDay(), Name: h.GetName()}
	}
	days, err := domain.NormalizeSupportHolidays(in, s.now())
	if err != nil {
		return nil, err
	}
	by := strings.TrimSpace(req.GetAuthorName())
	if by == "" {
		by = "Payrudder support"
	}
	var out []*plansv1.SupportHoliday
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.SupportAppName(ctx, app); errors.Is(err, pgx.ErrNoRows) {
			return errkey.New(codes.NotFound, "support.app.not_found", "app not found")
		} else if err != nil {
			return err
		}
		if err := q.DeleteSupportHolidays(ctx, app); err != nil {
			return err
		}
		for _, d := range days {
			if err := q.InsertSupportHoliday(ctx, sqlc.InsertSupportHolidayParams{AppID: app, Day: d.Day, Name: d.Name}); err != nil {
				return err
			}
		}
		rows, err := q.ListSupportHolidays(ctx, app)
		if err != nil {
			return err
		}
		out = holidaysOf(rows)
		return audit.Record(ctx, tx, audit.Event{Action: "support.holidays_set", TargetType: "app", TargetID: app.String(), Summary: fmt.Sprintf("support set %d days off for an app", len(days)),
			Detail: map[string]any{"days": len(days), "by": by}})
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.SetSupportHolidaysResponse{Holidays: out}, nil
}
