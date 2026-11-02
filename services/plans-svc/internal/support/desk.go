package support

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// The operator's desk: the unread badge of the person, the report of the queue, who a ticket can be assigned to, and the canned replies.

// reportWindowDays is how far back the report looks at first answers.
const reportWindowDays = 30

// CountMySupportUnread is the number of the person's tickets with news they have not read.
func (s *Service) CountMySupportUnread(ctx context.Context, req *plansv1.CountMySupportUnreadRequest) (*plansv1.CountMySupportUnreadResponse, error) {
	user, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, notFound()
	}
	n, err := sqlc.New(s.pool).SupportUnreadCount(ctx, user)
	if err != nil {
		return nil, err
	}
	return &plansv1.CountMySupportUnreadResponse{Unread: n}, nil
}

func counts(rows []sqlc.SupportCountsByStatusRow) []*plansv1.SupportCount {
	out := make([]*plansv1.SupportCount, len(rows))
	for i, r := range rows {
		out[i] = &plansv1.SupportCount{Value: r.Value, Count: r.Count}
	}
	return out
}

// GetSupportReport counts the queue and the first answers of the last 30 days.
func (s *Service) GetSupportReport(ctx context.Context, _ *plansv1.GetSupportReportRequest) (*plansv1.GetSupportReportResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	q := sqlc.New(s.pool)
	queue, err := q.SupportQueueCounts(ctx)
	if err != nil {
		return nil, err
	}
	byStatus, err := q.SupportCountsByStatus(ctx)
	if err != nil {
		return nil, err
	}
	byPriority, err := q.SupportActiveCountsByPriority(ctx)
	if err != nil {
		return nil, err
	}
	fa, err := q.SupportFirstAnswerStats(ctx, reportWindowDays)
	if err != nil {
		return nil, err
	}
	pri := make([]*plansv1.SupportCount, len(byPriority))
	for i, r := range byPriority {
		pri[i] = &plansv1.SupportCount{Value: r.Value, Count: r.Count}
	}
	return &plansv1.GetSupportReportResponse{Waiting: queue.Waiting, Breached: queue.Breached, ByStatus: counts(byStatus), ByPriority: pri, WindowDays: reportWindowDays,
		Opened: fa.Opened, Answered: fa.Answered, AnsweredOnTime: fa.AnsweredOnTime, MedianFirstAnswerSeconds: fa.MedianSeconds, AverageFirstAnswerSeconds: fa.AverageSeconds}, nil
}

// ListSupportOperators lists the addresses a ticket can be assigned to.
func (s *Service) ListSupportOperators(ctx context.Context, _ *plansv1.ListSupportOperatorsRequest) (*plansv1.ListSupportOperatorsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	names := map[string]string{}
	if len(s.cfg.Operators) > 0 {
		rows, err := sqlc.New(s.pool).SupportOperatorNames(ctx, s.cfg.Operators)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			names[r.Email] = r.Name
		}
	}
	out := make([]*plansv1.SupportOperator, len(s.cfg.Operators))
	for i, e := range s.cfg.Operators {
		out[i] = &plansv1.SupportOperator{Email: e, Name: names[e]}
	}
	return &plansv1.ListSupportOperatorsResponse{Operators: out}, nil
}

func cannedOf(id uuid.UUID, title, body, by string, at time.Time) *plansv1.SupportCannedReply {
	return &plansv1.SupportCannedReply{Id: id.String(), Title: title, Body: body, UpdatedBy: by, UpdateTime: timestamppb.New(at)}
}

func cannedNotFound() error {
	return errkey.New(codes.NotFound, "support.canned_reply.not_found", "canned reply not found")
}

// ListSupportCannedReplies lists the operators' shared snippets by title.
func (s *Service) ListSupportCannedReplies(ctx context.Context, _ *plansv1.ListSupportCannedRepliesRequest) (*plansv1.ListSupportCannedRepliesResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.pool).ListSupportCannedReplies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*plansv1.SupportCannedReply, len(rows))
	for i, r := range rows {
		out[i] = cannedOf(r.ID, r.Title, r.Body, r.UpdatedBy, r.UpdatedAt)
	}
	return &plansv1.ListSupportCannedRepliesResponse{Replies: out}, nil
}

// SaveSupportCannedReply makes a canned reply, or changes the one named by id.
func (s *Service) SaveSupportCannedReply(ctx context.Context, req *plansv1.SaveSupportCannedReplyRequest) (*plansv1.SaveSupportCannedReplyResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	title, body := strings.TrimSpace(req.GetTitle()), strings.TrimSpace(req.GetBody())
	if err := domain.ValidateSupportCannedReply(title, body); err != nil {
		return nil, err
	}
	by := strings.TrimSpace(req.GetAuthorName())
	if by == "" {
		by = "Payrudder support"
	}
	q := sqlc.New(s.pool)
	if req.GetId() == "" {
		r, err := q.InsertSupportCannedReply(ctx, sqlc.InsertSupportCannedReplyParams{ID: uuid.New(), Title: title, Body: body, UpdatedBy: by})
		if err != nil {
			return nil, err
		}
		return &plansv1.SaveSupportCannedReplyResponse{Reply: cannedOf(r.ID, r.Title, r.Body, r.UpdatedBy, r.UpdatedAt)}, nil
	}
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, cannedNotFound()
	}
	r, err := q.UpdateSupportCannedReply(ctx, sqlc.UpdateSupportCannedReplyParams{ID: id, Title: title, Body: body, UpdatedBy: by})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, cannedNotFound()
	}
	if err != nil {
		return nil, err
	}
	return &plansv1.SaveSupportCannedReplyResponse{Reply: cannedOf(r.ID, r.Title, r.Body, r.UpdatedBy, r.UpdatedAt)}, nil
}

// DeleteSupportCannedReply removes one.
func (s *Service) DeleteSupportCannedReply(ctx context.Context, req *plansv1.DeleteSupportCannedReplyRequest) (*plansv1.DeleteSupportCannedReplyResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, cannedNotFound()
	}
	n, err := sqlc.New(s.pool).DeleteSupportCannedReply(ctx, id)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, cannedNotFound()
	}
	return &plansv1.DeleteSupportCannedReplyResponse{}, nil
}

// RegisterMetrics exposes the queue as gauges on the service's metrics: support_waiting_tickets (open tickets) and support_sla_breached_tickets{priority} (open
// tickets with no first answer past their deadline), which the alert SupportFirstAnswerOverdue watches (deploy/prometheus/rules). They are read from the database when
// Prometheus scrapes, at most once every 15 seconds; a read that fails reports nothing (and the alert's `for` rides over a gap).
func (s *Service) RegisterMetrics() error {
	m := otel.Meter("github.com/0furkancolak/payrudder/services/plans-svc/support")
	waiting, err := m.Int64ObservableGauge("support_waiting_tickets", metric.WithDescription("Support tickets waiting for the operator (open)"))
	if err != nil {
		return err
	}
	breached, err := m.Int64ObservableGauge("support_sla_breached_tickets", metric.WithDescription("Open support tickets with no first answer past their deadline, by priority"))
	if err != nil {
		return err
	}
	var (
		mu   sync.Mutex
		at   time.Time
		last struct {
			waiting int64
			by      map[string]int64
		}
	)
	_, err = m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) > 15*time.Second {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			q := sqlc.New(s.pool)
			queue, err := q.SupportQueueCounts(cctx)
			if err != nil {
				s.log.WarnContext(ctx, "support metrics: the queue could not be counted", "error", err)
				return nil
			}
			rows, err := q.SupportBreachedByPriority(cctx)
			if err != nil {
				s.log.WarnContext(ctx, "support metrics: the late tickets could not be counted", "error", err)
				return nil
			}
			last.waiting, last.by = int64(queue.Waiting), map[string]int64{}
			for _, p := range domain.SupportPriorities {
				last.by[p] = 0 // a priority with none is a 0, so an alert resolves instead of going stale
			}
			for _, r := range rows {
				last.by[r.Priority] = int64(r.Count)
			}
			at = time.Now()
		}
		o.ObserveInt64(waiting, last.waiting)
		for p, n := range last.by {
			o.ObserveInt64(breached, n, metric.WithAttributes(attribute.String("priority", p)))
		}
		return nil
	}, waiting, breached)
	return err
}
