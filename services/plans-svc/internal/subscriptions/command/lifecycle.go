package command

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/pkg/tenant"
	changescmd "github.com/0furkancolak/quotify/services/plans-svc/internal/changes/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	entitlementscmd "github.com/0furkancolak/quotify/services/plans-svc/internal/entitlements/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/quotify/services/plans-svc/internal/subscriptions/query"
	tenantsqry "github.com/0furkancolak/quotify/services/plans-svc/internal/tenants/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/webhooks"
)

// Cancelled is the outcome of Cancel: the subscription and the unused fee credited to the customer.
type Cancelled struct {
	Subscription  *plansv1.Subscription
	CreditedMinor int64
}

// Cancel ends the subscription now or at the end time the request names, optionally crediting the unused prepaid fee.
func (c *Commands) Cancel(ctx context.Context, req *plansv1.CancelSubscriptionRequest) (Cancelled, error) {
	id, err := subsqry.ParseID(req.GetSubscriptionId())
	if err != nil {
		return Cancelled{}, err
	}
	end := c.now()
	if req.GetEndTime() != nil {
		end = req.GetEndTime().AsTime()
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return Cancelled{}, err
	}
	if req.GetCreditUnused() && end.After(c.now()) {
		return Cancelled{}, domain.Invalid("credit_unused", "needs a subscription that ends now; schedule the cancellation for the end of the period instead")
	}
	var out *plansv1.Subscription
	var credited int64
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		if err := requireCancellable(row, end, req.GetCreditUnused()); err != nil {
			return err
		}
		n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: id, EndTime: end})
		if err != nil {
			return err
		}
		if n != 1 {
			return status.Error(codes.Aborted, "subscription changed concurrently; retry")
		}
		if req.GetCreditUnused() {
			// The row as it was before it ended: it still says what was prepaid.
			if credited, err = changescmd.CreditUnusedFee(ctx, q, c.now(), row, end); err != nil {
				return err
			}
		}
		if !end.After(c.now()) {
			if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: id, State: string(subscription.Cancelled), PausedAt: cur.PausedAt}); err != nil {
				return err
			}
		}
		r, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = subsqry.Message(r, c.now())
		if err := c.emitCancellation(ctx, q, out, end, credited); err != nil {
			return err
		}
		return entitlementscmd.Publish(ctx, q, ti.ID, r.CustomerID, r.PlanVersionID, r.StartTime, &end, c.now())
	})
	if err != nil {
		return Cancelled{}, convert.Err(err, "subscription")
	}
	return Cancelled{Subscription: out, CreditedMinor: credited}, nil
}

// requireCancellable refuses a subscription that cannot end at end, or whose unused fee cannot be credited.
func requireCancellable(row sqlc.GetSubscriptionRow, end time.Time, creditUnused bool) error {
	if err := domain.RequireQuotifyOwned(row.Owner, row.Provider); err != nil {
		return err
	}
	if !row.OpenEnded {
		return status.Error(codes.FailedPrecondition, "subscription already ended")
	}
	if creditUnused && row.FeeTiming != "advance" {
		return status.Error(codes.FailedPrecondition, "this subscription is billed in arrears: no fee was paid in advance")
	}
	if !end.After(row.StartTime) {
		return domain.Invalid("end_time", "must be after the subscription start")
	}
	if _, err := subscription.Next(subscription.State(row.State), subscription.Cancel); err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return nil
}

// emitCancellation tells the tenant's webhooks that the subscription ended, or will end, at end.
func (c *Commands) emitCancellation(ctx context.Context, q *sqlc.Queries, sub *plansv1.Subscription, end time.Time, credited int64) error {
	typ := webhooks.SubscriptionCancelled
	if end.After(c.now()) {
		typ = webhooks.SubscriptionCancelScheduled
	}
	extra := map[string]any(nil)
	if credited > 0 {
		extra = map[string]any{"credited_minor": strconv.FormatInt(credited, 10)}
	}
	return webhooks.EmitSubscription(ctx, q, c.now(), typ, sub, extra)
}

// move applies event to the subscription's effective state and stores the result.
func (c *Commands) move(ctx context.Context, id uuid.UUID, ev subscription.Event, mutate func(sqlc.GetSubscriptionRow, subscription.State) (paused *time.Time, cancelAtEnd bool)) (*plansv1.Subscription, error) {
	var out *plansv1.Subscription
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		if err := domain.RequireQuotifyOwned(row.Owner, row.Provider); err != nil {
			return err
		}
		if !row.OpenEnded {
			return status.Error(codes.FailedPrecondition, "subscription already ended")
		}
		eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, c.now())
		next, err := subscription.Next(eff, ev)
		if err != nil {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		paused, cancelAtEnd := mutate(row, next)
		if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{
			ID: id, State: string(next), PausedAt: paused, CancelAtPeriodEnd: cancelAtEnd,
		}); err != nil {
			return err
		}
		r, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = subsqry.Message(r, c.now())
		switch ev {
		case subscription.Pause:
			return webhooks.EmitSubscription(ctx, q, c.now(), webhooks.SubscriptionPaused, out, nil)
		case subscription.Resume:
			return webhooks.EmitSubscription(ctx, q, c.now(), webhooks.SubscriptionResumed, out, nil)
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

// Pause stops billing the subscription until it is resumed.
func (c *Commands) Pause(ctx context.Context, req *plansv1.PauseSubscriptionRequest) (*plansv1.Subscription, error) {
	id, err := subsqry.ParseID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	now := c.now()
	return c.move(ctx, id, subscription.Pause, func(r sqlc.GetSubscriptionRow, _ subscription.State) (*time.Time, bool) {
		return &now, r.CancelAtPeriodEnd
	})
}

// Resume undoes a pause.
func (c *Commands) Resume(ctx context.Context, req *plansv1.ResumeSubscriptionRequest) (*plansv1.Subscription, error) {
	id, err := subsqry.ParseID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	return c.move(ctx, id, subscription.Resume, func(r sqlc.GetSubscriptionRow, _ subscription.State) (*time.Time, bool) {
		return nil, r.CancelAtPeriodEnd
	})
}

// ScheduleCancellation makes the subscription end with its current billing period, or withdraws that.
func (c *Commands) ScheduleCancellation(ctx context.Context, req *plansv1.ScheduleCancellationRequest) (*plansv1.Subscription, error) {
	id, err := subsqry.ParseID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		t, err := tenantsqry.Current(ctx, q)
		if err != nil {
			return err
		}
		loc, err := tenantsqry.Location(t.Timezone)
		if err != nil {
			return err
		}
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		if err := domain.RequireQuotifyOwned(row.Owner, row.Provider); err != nil {
			return err
		}
		now := c.now()
		if req.GetCancel() {
			err = scheduleEnd(ctx, q, ti.ID, row, loc, now)
		} else {
			err = withdrawScheduledEnd(ctx, q, ti.ID, row, now)
		}
		if err != nil {
			return err
		}
		r, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = subsqry.Message(r, now)
		if req.GetCancel() {
			return webhooks.EmitSubscription(ctx, q, c.now(), webhooks.SubscriptionCancelScheduled, out, nil)
		}
		return webhooks.EmitSubscription(ctx, q, c.now(), webhooks.SubscriptionCancelWithdrawn, out, nil)
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

// currentPeriod returns the billing period of sub that contains at.
func currentPeriod(sub sqlc.GetSubscriptionRow, loc *time.Location, at time.Time) (start, end time.Time, err error) {
	n, err := subscription.PeriodIndex(sub.BillingAnchor, subscription.Interval(sub.Interval), at, loc)
	if err != nil {
		return start, end, status.Error(codes.FailedPrecondition, err.Error())
	}
	if start, err = subscription.PeriodStart(sub.BillingAnchor, subscription.Interval(sub.Interval), n, loc); err != nil {
		return start, end, err
	}
	end, err = subscription.PeriodEnd(sub.BillingAnchor, subscription.Interval(sub.Interval), n, loc)
	return start, end, err
}

// scheduleEnd makes the subscription end with its current billing period.
func scheduleEnd(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, row sqlc.GetSubscriptionRow, loc *time.Location, now time.Time) error {
	if !row.OpenEnded {
		return status.Error(codes.FailedPrecondition, "subscription already ended or already scheduled to end")
	}
	eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, now)
	if _, err := subscription.Next(eff, subscription.PeriodEndedCancelling); err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	// A subscription that has not started yet ends with its first period.
	ref := now
	if ref.Before(row.StartTime) {
		ref = row.StartTime
	}
	_, end, err := currentPeriod(row, loc, ref)
	if err != nil {
		return err
	}
	if n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: row.ID, EndTime: end}); err != nil {
		return err
	} else if n != 1 {
		return status.Error(codes.Aborted, "subscription changed concurrently; retry")
	}
	if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: row.ID, State: row.State, PausedAt: row.PausedAt, CancelAtPeriodEnd: true}); err != nil {
		return err
	}
	return entitlementscmd.Publish(ctx, q, tenantID, row.CustomerID, row.PlanVersionID, row.StartTime, &end, now)
}

// withdrawScheduledEnd reopens a subscription that was scheduled to end.
func withdrawScheduledEnd(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, row sqlc.GetSubscriptionRow, now time.Time) error {
	if !row.CancelAtPeriodEnd || row.OpenEnded || !row.EndTime.After(now) {
		return status.Error(codes.FailedPrecondition, "no scheduled cancellation to withdraw")
	}
	if n, err := q.ReopenSubscription(ctx, sqlc.ReopenSubscriptionParams{ID: row.ID, Now: now}); err != nil {
		return err
	} else if n != 1 {
		return status.Error(codes.Aborted, "subscription changed concurrently; retry")
	}
	if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: row.ID, State: row.State, PausedAt: row.PausedAt, CancelAtPeriodEnd: false}); err != nil {
		return err
	}
	return entitlementscmd.Publish(ctx, q, tenantID, row.CustomerID, row.PlanVersionID, row.StartTime, nil, now)
}
