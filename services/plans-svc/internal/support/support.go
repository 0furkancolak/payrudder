// Package support is the help desk (SupportService): tickets that the people running apps open with the operator, the thread on each, and the operator's
// hand on status, priority and assignee. It runs on the system pool; a person reaches only the tickets of the accounts they belong to, and the operator calls
// need the operator context.
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
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Service implements plansv1.SupportServiceServer.
type Service struct {
	plansv1.UnimplementedSupportServiceServer
	pool *pgxpool.Pool
	log  *slog.Logger
	now  func() time.Time
}

// New returns the help desk on the system pool.
func New(pool *pgxpool.Pool, log *slog.Logger) *Service {
	return &Service{pool: pool, log: log, now: time.Now}
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
		SolveTime: ts(r.SolvedAt), MessageCount: r.MessageCount,
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

func stateOf(r sqlc.LockSupportTicketRow) domain.SupportTicketState {
	return domain.SupportTicketState{Status: r.Status, Priority: r.Priority, Assignee: r.Assignee, FirstResponseDue: r.FirstResponseDue, FirstRespondedAt: r.FirstRespondedAt, SolvedAt: r.SolvedAt, CreatedAt: r.CreatedAt}
}

func save(ctx context.Context, q *sqlc.Queries, id uuid.UUID, s domain.SupportTicketState, lastAuthor string, touch bool) error {
	return q.SaveSupportTicket(ctx, sqlc.SaveSupportTicketParams{ID: id, Status: s.Status, Priority: s.Priority, Assignee: s.Assignee, LastAuthor: lastAuthor, FirstResponseDue: s.FirstResponseDue,
		FirstRespondedAt: s.FirstRespondedAt, SolvedAt: s.SolvedAt, Touch: touch})
}

// ticket reads one ticket as the proto message.
func ticket(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (*plansv1.SupportTicket, error) {
	r, err := q.GetSupportTicket(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound()
		}
		return nil, err
	}
	return ticketOf(r), nil
}

func list(ctx context.Context, q *sqlc.Queries, p sqlc.ListSupportTicketsParams) ([]*plansv1.SupportTicket, error) {
	rows, err := q.ListSupportTickets(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]*plansv1.SupportTicket, len(rows))
	for i, r := range rows {
		out[i] = ticketOf(sqlc.GetSupportTicketRow(r))
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
		id := uuid.New()
		if err := q.InsertSupportTicket(ctx, sqlc.InsertSupportTicketParams{ID: id, AccountID: account, AppID: appID, OpenedBy: user, Subject: strings.TrimSpace(req.GetSubject()), Category: req.GetCategory(),
			Priority: priority, FirstResponseDue: s.now().Add(domain.SupportFirstResponse(priority))}); err != nil {
			return err
		}
		t, err := ticket(ctx, q, id)
		if err != nil {
			return err
		}
		if err := q.InsertSupportMessage(ctx, sqlc.InsertSupportMessageParams{ID: uuid.New(), TicketID: id, AuthorKind: "customer", AuthorUserID: &user, AuthorName: t.GetOpenedByName(), Body: strings.TrimSpace(req.GetBody())}); err != nil {
			return err
		}
		out, err = ticket(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, err
	}
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
	t, err := ticket(ctx, q, id)
	if err != nil {
		return nil, err
	}
	account, _ := uuid.Parse(t.GetAccountId())
	if err := member(ctx, q, user, account); err != nil {
		return nil, err
	}
	msgs, err := q.ListSupportMessages(ctx, sqlc.ListSupportMessagesParams{TicketID: id})
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMySupportTicketResponse{Ticket: t, Messages: messagesOf(msgs)}, nil
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
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := lockedFor(ctx, q, id, &user)
		if err != nil {
			return err
		}
		next, ok := domain.SupportCustomerReply(stateOf(row))
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
		if err := save(ctx, q, id, next, "customer", true); err != nil {
			return err
		}
		out, err = ticket(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, err
	}
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
		next, err := domain.SupportSetStatus(stateOf(row), req.GetStatus(), false, s.now())
		if err != nil {
			return err
		}
		if err := save(ctx, q, id, next, lastAuthorOf(ctx, q, id), false); err != nil {
			return err
		}
		out, err = ticket(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.SetMySupportTicketStatusResponse{Ticket: out}, nil
}

// lastAuthorOf keeps the ticket's last author when only its status changes.
func lastAuthorOf(ctx context.Context, q *sqlc.Queries, id uuid.UUID) string {
	t, err := ticket(ctx, q, id)
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
		Search: likeEscaped(req.GetSearch()), SlaBreached: req.GetSlaBreached()})
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
	t, err := ticket(ctx, q, id)
	if err != nil {
		return nil, err
	}
	msgs, err := q.ListSupportMessages(ctx, sqlc.ListSupportMessagesParams{TicketID: id, WithInternal: true})
	if err != nil {
		return nil, err
	}
	return &plansv1.GetSupportTicketResponse{Ticket: t, Messages: messagesOf(msgs)}, nil
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
			// Only a public answer is an answer: it stops the SLA clock, hands the ticket to the person and counts as activity.
			if err := save(ctx, q, id, domain.SupportOperatorReply(stateOf(row), s.now()), "operator", true); err != nil {
				return err
			}
		}
		out, err = ticket(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, err
	}
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
	var out *plansv1.SupportTicket
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := lockedFor(ctx, q, id, nil)
		if err != nil {
			return err
		}
		next := stateOf(row)
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
			if err := domain.ValidateSupportAssignee(req.GetAssignee()); err != nil {
				return err
			}
			next.Assignee = strings.TrimSpace(req.GetAssignee())
		}
		if err := save(ctx, q, id, next, lastAuthorOf(ctx, q, id), false); err != nil {
			return err
		}
		out, err = ticket(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.UpdateSupportTicketResponse{Ticket: out}, nil
}
