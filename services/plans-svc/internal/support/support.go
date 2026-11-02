// Package support is the help desk (SupportService): tickets that the people running apps open with the operator, the thread on each, and the operator's
// hand on status, priority and assignee. It runs on the system pool; a person reaches only the tickets of the accounts they belong to, and the operator calls
// need the operator context. A new ticket is mailed to the operators, an answer and a change of status to the person who opened it (usermail, in their language),
// every change is kept in an append-only history, and a ticket that waited for the person for days closes by itself (AutoClose).
package support

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Config is what the help desk needs besides its database.
type Config struct {
	// Mail sends the notices; nil sends none.
	Mail usermail.Sender
	// Operators are the lower-case addresses that get a mail for a new ticket (PAYRUDDER_OPERATOR_EMAILS, the list the gateway lets into the operator console).
	Operators []string
	// AppURL is the console's address; the links in the notices open the ticket in it.
	AppURL string
}

// Service implements plansv1.SupportServiceServer.
type Service struct {
	plansv1.UnimplementedSupportServiceServer
	pool *pgxpool.Pool
	log  *slog.Logger
	now  func() time.Time
	cfg  Config
}

// New returns the help desk on the system pool.
func New(pool *pgxpool.Pool, log *slog.Logger, cfg Config) *Service {
	ops := make([]string, 0, len(cfg.Operators))
	for _, o := range cfg.Operators {
		if o = strings.ToLower(strings.TrimSpace(o)); o != "" {
			ops = append(ops, o)
		}
	}
	cfg.Operators = ops
	return &Service{pool: pool, log: log, now: time.Now, cfg: cfg}
}

func notFound() error {
	return errkey.New(codes.NotFound, "support.ticket.not_found", "ticket not found")
}

func parse(id string) (uuid.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, notFound()
	}
	return u, nil
}

// member reports whether the user belongs (not as an affiliate) to the account.
func member(ctx context.Context, q *sqlc.Queries, user, account uuid.UUID) error {
	if _, err := q.SupportRoleOf(ctx, sqlc.SupportRoleOfParams{AccountID: account, UserID: user}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound() // not the person's ticket: the same answer as a ticket that is not there
		}
		return err
	}
	return nil
}

func ts(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func ticketOf(r sqlc.GetSupportTicketRow) *plansv1.SupportTicket {
	t := &plansv1.SupportTicket{
		Id: r.ID.String(), AccountId: r.AccountID.String(), AccountName: r.AccountName, AppName: r.AppName, OpenedBy: r.OpenedBy.String(), OpenedByName: r.OpenedByName,
		Subject: r.Subject, Category: r.Category, Priority: r.Priority, Status: r.Status, Assignee: r.Assignee, LastAuthor: r.LastAuthor,
		CreateTime: timestamppb.New(r.CreatedAt), UpdateTime: timestamppb.New(r.UpdatedAt), LastMessageTime: timestamppb.New(r.LastMessageAt),
		FirstResponseDue: timestamppb.New(r.FirstResponseDue), FirstResponseTime: ts(r.FirstRespondedAt), SlaBreached: r.SlaBreached,
		SolveTime: ts(r.SolvedAt), MessageCount: r.MessageCount, Unread: r.Unread, Tags: r.Tags,
	}
	if r.Number != nil {
		t.Number = *r.Number
	}
	if r.AppID != nil {
		t.AppId = r.AppID.String()
	}
	return t
}

func messagesOf(rows []sqlc.ListSupportMessagesRow) []*plansv1.SupportMessage {
	out := make([]*plansv1.SupportMessage, len(rows))
	for i, m := range rows {
		out[i] = &plansv1.SupportMessage{Id: m.ID.String(), AuthorKind: m.AuthorKind, AuthorName: m.AuthorName, Body: m.Body, Internal: m.Internal, CreateTime: timestamppb.New(m.CreatedAt)}
	}
	return out
}

func eventsOf(rows []sqlc.ListSupportEventsRow) []*plansv1.SupportEvent {
	out := make([]*plansv1.SupportEvent, len(rows))
	for i, e := range rows {
		out[i] = &plansv1.SupportEvent{Id: e.ID.String(), Kind: e.Kind, ActorKind: e.ActorKind, ActorName: e.ActorName, FromValue: e.FromValue, ToValue: e.ToValue, CreateTime: timestamppb.New(e.CreatedAt)}
	}
	return out
}

func stateOf(r sqlc.LockSupportTicketRow) domain.SupportTicketState {
	return domain.SupportTicketState{Status: r.Status, Priority: r.Priority, Assignee: r.Assignee, Tags: r.Tags, FirstResponseDue: r.FirstResponseDue, FirstRespondedAt: r.FirstRespondedAt, SolvedAt: r.SolvedAt,
		CreatedAt: r.CreatedAt, Zone: r.SlaZone}
}

// actor is who made a change, as the history shows it.
type actor struct{ kind, name string }

// save writes the new state of a ticket and, for each of status, priority, assignee and tags that differs from prev, a line in the history.
func save(ctx context.Context, q *sqlc.Queries, id uuid.UUID, prev, next domain.SupportTicketState, lastAuthor string, touch bool, by actor) error {
	if next.Tags == nil {
		next.Tags = []string{}
	}
	if err := q.SaveSupportTicket(ctx, sqlc.SaveSupportTicketParams{ID: id, Status: next.Status, Priority: next.Priority, Assignee: next.Assignee, Tags: next.Tags, LastAuthor: lastAuthor, FirstResponseDue: next.FirstResponseDue,
		FirstRespondedAt: next.FirstRespondedAt, SolvedAt: next.SolvedAt, Touch: touch}); err != nil {
		return err
	}
	for _, c := range []struct{ kind, from, to string }{{"status", prev.Status, next.Status}, {"priority", prev.Priority, next.Priority}, {"assignee", prev.Assignee, next.Assignee}, {"tags", strings.Join(prev.Tags, ","), strings.Join(next.Tags, ",")}} {
		if c.from == c.to {
			continue
		}
		if err := q.InsertSupportEvent(ctx, sqlc.InsertSupportEventParams{ID: idgen.NewUUIDv7(), TicketID: id, Kind: c.kind, ActorKind: by.kind, ActorName: by.name, FromValue: c.from, ToValue: c.to}); err != nil {
			return err
		}
	}
	return nil
}

// ticket reads one ticket as the proto message; viewer is the person looking (their "unread" mark), nil for the operator and for the code that only needs the facts.
func ticket(ctx context.Context, q *sqlc.Queries, id uuid.UUID, viewer *uuid.UUID) (*plansv1.SupportTicket, error) {
	r, err := q.GetSupportTicket(ctx, sqlc.GetSupportTicketParams{ID: id, Viewer: viewer})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound()
		}
		return nil, err
	}
	t := ticketOf(r)
	if viewer != nil {
		t.Tags = nil // the operator's labels are not the person's to read
	}
	return t, nil
}

func list(ctx context.Context, q *sqlc.Queries, p sqlc.ListSupportTicketsParams) ([]*plansv1.SupportTicket, error) {
	rows, err := q.ListSupportTickets(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]*plansv1.SupportTicket, len(rows))
	for i, r := range rows {
		out[i] = ticketOf(sqlc.GetSupportTicketRow(r))
		if p.UserID != nil {
			out[i].Tags = nil // the operator's labels are not the person's to read
		}
	}
	return out, nil
}

// likeEscaped keeps %, _ and \ in a search term from being patterns.
func likeEscaped(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(s))
}

// --- the person's side

func (s *Service) OpenMySupportTicket(ctx context.Context, req *plansv1.OpenMySupportTicketRequest) (*plansv1.OpenMySupportTicketResponse, error) {
	user, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, notFound()
	}
	account, err := uuid.Parse(req.GetAccountId())
	if err != nil {
		return nil, notFound()
	}
	priority := req.GetPriority()
	if priority == "" {
		priority = "normal"
	}
	if err := domain.ValidateSupportTicket(req.GetSubject(), req.GetCategory(), priority, req.GetBody()); err != nil {
		return nil, err
	}
	var appID *uuid.UUID
	if req.GetAppId() != "" {
		a, err := uuid.Parse(req.GetAppId())
		if err != nil {
			return nil, errkey.New(codes.NotFound, "support.app.not_found", "app not found")
		}
		appID = &a
	}
	var out *plansv1.SupportTicket
	var mails []usermail.Mail
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := member(ctx, q, user, account); err != nil {
			return errkey.New(codes.PermissionDenied, "support.account.not_member", "you are not a member of this account")
		}
		if appID != nil {
			ok, err := q.SupportAppInAccount(ctx, sqlc.SupportAppInAccountParams{ID: *appID, AccountID: account})
			if err != nil {
				return err
			}
			if !ok {
				return errkey.New(codes.NotFound, "support.app.not_found", "app not found")
			}
		}
		// The count and the insert are one step per account: a second ticket opened at the same moment waits for this one.
		if err := q.SupportLockAccount(ctx, account.String()); err != nil {
			return err
		}
		if n, err := q.SupportOpenCount(ctx, account); err != nil {
			return err
		} else if n >= domain.SupportMaxOpen {
			return errkey.Newf(codes.ResourceExhausted, "support.tickets.too_many", "an account can have at most %d tickets waiting at once: wait for an answer or mark one solved", domain.SupportMaxOpen)
		}
		zone, err := q.SupportZoneFor(ctx, sqlc.SupportZoneForParams{AccountID: account, AppID: appID})
		if err != nil {
			return err
		}
		id := uuid.New()
		if err := q.InsertSupportTicket(ctx, sqlc.InsertSupportTicketParams{ID: id, AccountID: account, AppID: appID, OpenedBy: user, Subject: strings.TrimSpace(req.GetSubject()), Category: req.GetCategory(),
			Priority: priority, FirstResponseDue: domain.SupportFirstResponseDue(s.now(), priority, zone), SlaZone: zone}); err != nil {
			return err
		}
		t, err := ticket(ctx, q, id, &user)
		if err != nil {
			return err
		}
		if err := q.InsertSupportMessage(ctx, sqlc.InsertSupportMessageParams{ID: uuid.New(), TicketID: id, AuthorKind: "customer", AuthorUserID: &user, AuthorName: t.GetOpenedByName(), Body: strings.TrimSpace(req.GetBody())}); err != nil {
			return err
		}
		out, err = ticket(ctx, q, id, &user)
		if err != nil {
			return err
		}
		mails, err = s.operatorMails(ctx, q, id, usermail.KindTicketNew)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.send(ctx, mails)
	s.log.InfoContext(ctx, "support ticket opened", "ticket", out.GetId(), "number", out.GetNumber(), "account_id", out.GetAccountId(), "priority", out.GetPriority())
	return &plansv1.OpenMySupportTicketResponse{Ticket: out}, nil
}

func (s *Service) ListMySupportTickets(ctx context.Context, req *plansv1.ListMySupportTicketsRequest) (*plansv1.ListMySupportTicketsResponse, error) {
	user, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, notFound()
	}
	if err := domain.ValidateSupportFilter(req.GetStatus(), ""); err != nil {
		return nil, err
	}
	tickets, err := list(ctx, sqlc.New(s.pool), sqlc.ListSupportTicketsParams{UserID: &user, Status: req.GetStatus()})
	if err != nil {
		return nil, err
	}
	return &plansv1.ListMySupportTicketsResponse{Tickets: tickets}, nil
}

func (s *Service) GetMySupportTicket(ctx context.Context, req *plansv1.GetMySupportTicketRequest) (*plansv1.GetMySupportTicketResponse, error) {
	user, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, notFound()
	}
	id, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	q := sqlc.New(s.pool)
	t, err := ticket(ctx, q, id, &user)
	if err != nil {
		return nil, err
	}
	account, _ := uuid.Parse(t.GetAccountId())
	if err := member(ctx, q, user, account); err != nil {
		return nil, err
	}
	// Opening the ticket is reading it: the "new" mark of the person who opened it goes (another member's reading leaves it).
	if err := q.MarkSupportTicketSeen(ctx, sqlc.MarkSupportTicketSeenParams{ID: id, UserID: user}); err != nil {
		return nil, err
	}
	t.Unread = false
	msgs, err := q.ListSupportMessages(ctx, sqlc.ListSupportMessagesParams{TicketID: id})
	if err != nil {
		return nil, err
	}
	events, err := q.ListSupportEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMySupportTicketResponse{Ticket: t, Messages: messagesOf(msgs), Events: eventsOf(events)}, nil
}

// lockedFor locks the ticket and, for a person, checks they belong to its account.
func lockedFor(ctx context.Context, q *sqlc.Queries, id uuid.UUID, user *uuid.UUID) (sqlc.LockSupportTicketRow, error) {
	row, err := q.LockSupportTicket(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return row, notFound()
		}
		return row, err
	}
	if user != nil {
		if err := member(ctx, q, *user, row.AccountID); err != nil {
			return row, err
		}
	}
	return row, nil
}

func (s *Service) ReplyMySupportTicket(ctx context.Context, req *plansv1.ReplyMySupportTicketRequest) (*plansv1.ReplyMySupportTicketResponse, error) {
	user, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, notFound()
	}
	id, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateSupportReply(req.GetBody()); err != nil {
		return nil, err
	}
	var out *plansv1.SupportTicket
	var mails []usermail.Mail
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := lockedFor(ctx, q, id, &user)
		if err != nil {
			return err
		}
		prev := stateOf(row)
		next, ok := domain.SupportCustomerReply(prev)
		if !ok {
			return errkey.New(codes.FailedPrecondition, "support.ticket.closed", "this ticket is closed; open a new one")
		}
		name, err := q.SupportAuthorName(ctx, user)
		if err != nil {
			return err
		}
		if err := q.InsertSupportMessage(ctx, sqlc.InsertSupportMessageParams{ID: uuid.New(), TicketID: id, AuthorKind: "customer", AuthorUserID: &user, AuthorName: name, Body: strings.TrimSpace(req.GetBody())}); err != nil {
			return err
		}
		if err := save(ctx, q, id, prev, next, "customer", true, actor{"customer", name}); err != nil {
			return err
		}
		// Writing on the ticket is reading it, for the person who opened it.
		if err := q.MarkSupportTicketSeen(ctx, sqlc.MarkSupportTicketSeenParams{ID: id, UserID: user}); err != nil {
			return err
		}
		out, err = ticket(ctx, q, id, &user)
		if err != nil {
			return err
		}
		// The operators are told when the person answers the operator (the ticket was waiting for them, or solved); a second message in a row adds no second mail,
		// the ticket is in the queue already.
		if row.LastAuthor == "operator" {
			mails, err = s.operatorMails(ctx, q, id, usermail.KindTicketCustomerReply)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.send(ctx, mails)
	return &plansv1.ReplyMySupportTicketResponse{Ticket: out}, nil
}

func (s *Service) SetMySupportTicketStatus(ctx context.Context, req *plansv1.SetMySupportTicketStatusRequest) (*plansv1.SetMySupportTicketStatusResponse, error) {
	user, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, notFound()
	}
	id, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.SupportTicket
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := lockedFor(ctx, q, id, &user)
		if err != nil {
			return err
		}
		prev := stateOf(row)
		next, err := domain.SupportSetStatus(prev, req.GetStatus(), false, s.now())
		if err != nil {
			return err
		}
		name, err := q.SupportAuthorName(ctx, user)
		if err != nil {
			return err
		}
		if err := save(ctx, q, id, prev, next, lastAuthorOf(ctx, q, id), false, actor{"customer", name}); err != nil {
			return err
		}
		out, err = ticket(ctx, q, id, &user)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.SetMySupportTicketStatusResponse{Ticket: out}, nil
}

// lastAuthorOf keeps the ticket's last author when only its status changes.
func lastAuthorOf(ctx context.Context, q *sqlc.Queries, id uuid.UUID) string {
	t, err := ticket(ctx, q, id, nil)
	if err != nil || t.GetLastAuthor() == "" {
		return "customer"
	}
	return t.GetLastAuthor()
}

// --- the operator's side

func (s *Service) ListSupportTickets(ctx context.Context, req *plansv1.ListSupportTicketsRequest) (*plansv1.ListSupportTicketsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	if err := domain.ValidateSupportFilter(req.GetStatus(), req.GetPriority()); err != nil {
		return nil, err
	}
	tickets, err := list(ctx, sqlc.New(s.pool), sqlc.ListSupportTicketsParams{Status: req.GetStatus(), Priority: req.GetPriority(), Assignee: req.GetAssignee(),
		Search: likeEscaped(req.GetSearch()), SlaBreached: req.GetSlaBreached(), Tag: strings.ToLower(strings.TrimSpace(req.GetTag()))})
	if err != nil {
		return nil, err
	}
	return &plansv1.ListSupportTicketsResponse{Tickets: tickets}, nil
}

func (s *Service) GetSupportTicket(ctx context.Context, req *plansv1.GetSupportTicketRequest) (*plansv1.GetSupportTicketResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	q := sqlc.New(s.pool)
	t, err := ticket(ctx, q, id, nil)
	if err != nil {
		return nil, err
	}
	msgs, err := q.ListSupportMessages(ctx, sqlc.ListSupportMessagesParams{TicketID: id, WithInternal: true})
	if err != nil {
		return nil, err
	}
	events, err := q.ListSupportEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetSupportTicketResponse{Ticket: t, Messages: messagesOf(msgs), Events: eventsOf(events)}, nil
}

func (s *Service) ReplySupportTicket(ctx context.Context, req *plansv1.ReplySupportTicketRequest) (*plansv1.ReplySupportTicketResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateSupportReply(req.GetBody()); err != nil {
		return nil, err
	}
	author := strings.TrimSpace(req.GetAuthorName())
	if author == "" {
		author = "Payrudder support"
	}
	var out *plansv1.SupportTicket
	var mails []usermail.Mail
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := lockedFor(ctx, q, id, nil)
		if err != nil {
			return err
		}
		if err := q.InsertSupportMessage(ctx, sqlc.InsertSupportMessageParams{ID: uuid.New(), TicketID: id, AuthorKind: "operator", AuthorName: author, Internal: req.GetInternal(), Body: strings.TrimSpace(req.GetBody())}); err != nil {
			return err
		}
		if !req.GetInternal() {
			// Only a public answer is an answer: it stops the SLA clock, hands the ticket to the person, counts as activity, is news to the person who opened the
			// ticket and is mailed to them.
			prev := stateOf(row)
			if err := save(ctx, q, id, prev, domain.SupportOperatorReply(prev, s.now()), "operator", true, actor{"operator", author}); err != nil {
				return err
			}
			if err := q.MarkSupportTicketUnseen(ctx, id); err != nil {
				return err
			}
			if mails, err = s.openerMail(ctx, q, id, usermail.KindTicketReply); err != nil {
				return err
			}
		}
		out, err = ticket(ctx, q, id, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.send(ctx, mails)
	return &plansv1.ReplySupportTicketResponse{Ticket: out}, nil
}

func (s *Service) UpdateSupportTicket(ctx context.Context, req *plansv1.UpdateSupportTicketRequest) (*plansv1.UpdateSupportTicketResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	by := strings.TrimSpace(req.GetAuthorName())
	if by == "" {
		by = "Payrudder support"
	}
	var out *plansv1.SupportTicket
	var mails []usermail.Mail
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := lockedFor(ctx, q, id, nil)
		if err != nil {
			return err
		}
		prev := stateOf(row)
		next := prev
		if req.Status != nil {
			if next, err = domain.SupportSetStatus(next, req.GetStatus(), true, s.now()); err != nil {
				return err
			}
		}
		if req.Priority != nil {
			if next, err = domain.SupportSetPriority(next, req.GetPriority()); err != nil {
				return err
			}
		}
		if req.Assignee != nil {
			a := strings.TrimSpace(req.GetAssignee())
			if err := domain.ValidateSupportAssignee(a, prev.Assignee, s.cfg.Operators); err != nil {
				return err
			}
			if a != prev.Assignee {
				a = strings.ToLower(a) // the operators' addresses are kept lower case; an assignee from before the list is kept as it was
			}
			next.Assignee = a
		}
		if req.GetSetTags() {
			if next.Tags, err = domain.NormalizeSupportTags(req.GetTags()); err != nil {
				return err
			}
		}
		if err := save(ctx, q, id, prev, next, lastAuthorOf(ctx, q, id), false, actor{"operator", by}); err != nil {
			return err
		}
		if next.Status != prev.Status {
			if err := audit.Record(ctx, tx, audit.Event{AccountID: row.AccountID, Action: "support.status_changed", TargetType: "support_ticket", TargetID: id.String(),
				Summary: "support moved a ticket from " + prev.Status + " to " + next.Status, Detail: map[string]any{"from": prev.Status, "to": next.Status, "by": by}}); err != nil {
				return err
			}
		}
		// A new status is news to the person who opened the ticket; priority and assignee are the operator's own business.
		if next.Status != prev.Status {
			if err := q.MarkSupportTicketUnseen(ctx, id); err != nil {
				return err
			}
			if mails, err = s.openerMail(ctx, q, id, usermail.KindTicketStatus); err != nil {
				return err
			}
		}
		out, err = ticket(ctx, q, id, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.send(ctx, mails)
	return &plansv1.UpdateSupportTicketResponse{Ticket: out}, nil
}
