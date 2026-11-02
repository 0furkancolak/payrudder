package command

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// DefaultRetentionDays is how long a banned tenant's data is kept before the retention job marks the deadline due (default, not agreed: 90 days; PAYRUDDER_BAN_RETENTION_DAYS).
const DefaultRetentionDays = 90

// maxScheduleAhead bounds how far ahead a status can be scheduled.
const maxScheduleAhead = 366 * 24 * time.Hour

// WithRetention sets the retention period of a banned tenant's data in days (0 records no deadline).
func (c *Commands) WithRetention(days int) *Commands {
	c.retentionDays = days
	return c
}

// WithClock sets the clock the commands read (tests).
func (c *Commands) WithClock(now func() time.Time) *Commands {
	c.now = now
	return c
}

// trackRetention keeps the retention deadline of a banned tenant: a ban starts the period, and leaving the ban ends it. It runs in the transaction of the change.
func (c *Commands) trackRetention(ctx context.Context, q *sqlc.Queries, id uuid.UUID, from, to string) error {
	switch {
	case to == domain.StatusBanned && from != domain.StatusBanned && c.retentionDays > 0:
		until := c.now().AddDate(0, 0, c.retentionDays)
		return q.SetTenantRetention(ctx, sqlc.SetTenantRetentionParams{LiveTenantID: id, RetentionUntil: &until})
	case from == domain.StatusBanned && to != domain.StatusBanned:
		return q.ClearTenantRetention(ctx, id)
	}
	return nil
}

func lifecycleOf(r sqlc.TenantLifecycle) *plansv1.TenantLifecycle {
	out := &plansv1.TenantLifecycle{ScheduledCategory: r.ScheduledCategory, ScheduledReason: r.ScheduledReason, ScheduledBy: r.ScheduledBy}
	if r.RetentionUntil != nil {
		out.RetentionUntil = timestamppb.New(*r.RetentionUntil)
	}
	if r.RetentionDueAt != nil {
		out.RetentionDueTime = timestamppb.New(*r.RetentionDueAt)
	}
	if r.ScheduledStatus != nil && r.ScheduledAt != nil {
		out.ScheduledStatus, out.ScheduledTime = *r.ScheduledStatus, timestamppb.New(*r.ScheduledAt)
	}
	return out
}

// Lifecycle returns the retention deadline and the schedule of a tenant; a tenant with neither has an empty one.
func (c *Commands) Lifecycle(ctx context.Context, tenantID string) (*plansv1.TenantLifecycle, error) {
	id, err := domain.ParseID("tenant_id", tenantID)
	if err != nil {
		return nil, err
	}
	return c.lifecycle(ctx, sqlc.New(c.pool), id)
}

func (c *Commands) lifecycle(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (*plansv1.TenantLifecycle, error) {
	r, err := q.GetTenantLifecycle(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return &plansv1.TenantLifecycle{}, nil
	}
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return lifecycleOf(r), nil
}

// Schedule puts a suspension or a freeze of an active live tenant at a future time, for the sweeper to apply as the operator "system". A ban is never scheduled (it needs a
// reason and, where the approval is on, a second operator). One schedule per tenant: a new one replaces the old.
func (c *Commands) Schedule(ctx context.Context, req *plansv1.ScheduleTenantStatusRequest) (*plansv1.TenantLifecycle, error) {
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	status := req.GetStatus()
	if status != domain.StatusSuspended && status != domain.StatusFrozen {
		return nil, errkey.New(codes.InvalidArgument, "plans.tenant.bad_status", "a scheduled status is suspended or frozen")
	}
	now := c.now()
	at := req.GetScheduleTime()
	if at == nil || !at.AsTime().After(now) || at.AsTime().After(now.Add(maxScheduleAhead)) {
		return nil, domain.Invalid("schedule_time", "must be in the future, within a year")
	}
	reason := strings.TrimSpace(req.GetReason())
	if utf8.RuneCountInString(reason) > 500 {
		return nil, domain.Invalid("reason", "at most 500 characters")
	}
	if cat := req.GetCategory(); cat != "" && !slices.Contains(StatusCategories, cat) {
		return nil, domain.Invalid("category", "nonpayment, fraud, abuse, legal, request or other")
	}
	actor := actorOf(req.GetActor())
	var out *plansv1.TenantLifecycle
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		cur, err := q.LockTenantForGoLive(ctx, id)
		if err != nil {
			return err
		}
		if cur.Environment != env.Live.String() || cur.Status != domain.StatusActive {
			return errkey.New(codes.FailedPrecondition, "plans.tenant.bad_transition", "only an active live tenant can have a suspension or a freeze scheduled")
		}
		when := at.AsTime()
		row, err := q.SetTenantSchedule(ctx, sqlc.SetTenantScheduleParams{LiveTenantID: id, ScheduledStatus: &status, ScheduledAt: &when, ScheduledCategory: req.GetCategory(), ScheduledReason: reason, ScheduledBy: actor})
		if err != nil {
			return err
		}
		out = lifecycleOf(row)
		return audit.Record(ctx, tx, audit.Event{TenantID: id, Action: "tenant.status_scheduled", TargetType: "tenant", TargetID: id.String(),
			Summary: "scheduled the status " + status + " for " + when.UTC().Format(time.RFC3339), Detail: map[string]any{"status": status, "at": when.UTC().Format(time.RFC3339), "category": req.GetCategory(), "reason": reason}})
	})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return out, nil
}

// CancelSchedule drops the schedule of a tenant. Nothing scheduled is not an error.
func (c *Commands) CancelSchedule(ctx context.Context, req *plansv1.CancelScheduledTenantStatusRequest) (*plansv1.TenantLifecycle, error) {
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.TenantLifecycle
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		n, err := q.ClearTenantSchedule(ctx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			if err := audit.Record(ctx, tx, audit.Event{TenantID: id, Action: "tenant.status_schedule_cancelled", TargetType: "tenant", TargetID: id.String(), Summary: "cancelled the scheduled status"}); err != nil {
				return err
			}
		}
		out, err = c.lifecycle(ctx, q, id)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return out, nil
}

func actorOf(a string) string {
	a = strings.TrimSpace(a)
	if a == "" {
		a = "operator"
	}
	if r := []rune(a); len(r) > maxActorRunes {
		a = string(r[:maxActorRunes])
	}
	return a
}

// ApplyDueSchedules applies the schedules whose time has come, at most a batch, and says how many it applied. A schedule is applied as the operator "system" to a tenant that
// is active; a tenant that is in any other status by then (held by hand, say) is left as it is. Either way the schedule is spent and the audit log says which.
func (c *Commands) ApplyDueSchedules(ctx context.Context) (applied int, err error) {
	rows, err := sqlc.New(c.pool).ListDueTenantSchedules(ctx, sqlc.ListDueTenantSchedulesParams{Now: c.now(), Batch: 100})
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		ok, err := c.applyOne(ctx, r)
		if err != nil {
			c.log.WarnContext(ctx, "scheduled status not applied", "tenant_id", r.LiveTenantID, "error", err)
			continue
		}
		if ok {
			applied++
		}
	}
	return applied, nil
}

func (c *Commands) applyOne(ctx context.Context, r sqlc.TenantLifecycle) (bool, error) {
	if r.ScheduledStatus == nil {
		return false, nil
	}
	want := domain.StatusFromDB(*r.ScheduledStatus)
	reason := r.ScheduledReason
	t, err := c.setStatus(ctx, &plansv1.SetTenantStatusRequest{TenantId: r.LiveTenantID.String(), Status: want, Category: r.ScheduledCategory, Reason: reason, Actor: "system"}, domain.StatusActive, false)
	if err != nil {
		return false, err
	}
	applied := t.GetStatus() == want
	action, summary := "tenant.status_schedule_applied", "applied the scheduled status "+*r.ScheduledStatus
	if !applied {
		action, summary = "tenant.status_schedule_skipped", "did not apply the scheduled status "+*r.ScheduledStatus+": the tenant was not active"
	}
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.ClearTenantSchedule(ctx, r.LiveTenantID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: r.LiveTenantID, Action: action, TargetType: "tenant", TargetID: r.LiveTenantID.String(), Summary: summary,
			Detail: map[string]any{"status": *r.ScheduledStatus, "scheduled_by": r.ScheduledBy}})
	})
	return applied, err
}

// MarkRetentionDue marks the banned tenants whose retention period has ended and records it in the audit log. It deletes nothing: what happens to a banned tenant's data
// afterwards is an operator's decision outside this job (plan 14).
func (c *Commands) MarkRetentionDue(ctx context.Context) (int, error) {
	var n int
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := sqlc.New(tx).MarkRetentionDue(ctx, c.now())
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := audit.Record(ctx, tx, audit.Event{TenantID: r.LiveTenantID, Action: "tenant.retention_due", TargetType: "tenant", TargetID: r.LiveTenantID.String(),
				Summary: "the retention period of the banned tenant has ended; nothing was deleted", Detail: map[string]any{"retention_until": r.RetentionUntil.UTC().Format(time.RFC3339)}}); err != nil {
				return err
			}
		}
		n = len(rows)
		return nil
	})
	return n, err
}

// RunLifecycle applies due schedules and marks due retention deadlines every interval until ctx ends.
func (c *Commands) RunLifecycle(ctx context.Context, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := c.ApplyDueSchedules(ctx); err != nil {
			c.log.ErrorContext(ctx, "scheduled statuses: listing failed", "error", err)
		} else if n > 0 {
			c.log.InfoContext(ctx, "scheduled statuses applied", "count", n)
		}
		if n, err := c.MarkRetentionDue(ctx); err != nil {
			c.log.ErrorContext(ctx, "retention deadlines: marking failed", "error", err)
		} else if n > 0 {
			c.log.WarnContext(ctx, "retention periods ended (nothing was deleted)", "count", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
