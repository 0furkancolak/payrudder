// Package command makes, schedules, withdraws and applies subscription changes, and records the money they move.
package command

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	changesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subscriptionscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/command"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	webhookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// Commands changes subscriptions from one plan version or interval to another.
type Commands struct {
	db  *store.DB
	now func() time.Time
}

// New returns the change commands.
func New(db *store.DB, now func() time.Time) *Commands { return &Commands{db: db, now: now} }

// Change moves a subscription billed in advance to another plan version and/or interval, now or at the end of the paid period.
func (c *Commands) Change(ctx context.Context, req *plansv1.ChangeSubscriptionRequest) (*plansv1.ChangeSubscriptionResponse, error) {
	id, err := domain.ParseID("subscription_id", req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	resp := &plansv1.ChangeSubscriptionResponse{}
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		pc, err := changesqry.Price(ctx, q, c.now(), row, req)
		if err != nil {
			return err
		}
		// A change replaces the one that was waiting.
		if pending, err := q.GetPendingChange(ctx, id); err == nil {
			if _, err := q.SettleChange(ctx, sqlc.SettleChangeParams{ID: pending.ID, State: "cancelled"}); err != nil {
				return err
			}
		}
		resp.Quote = pc.Message()
		qt := pc.Quote
		if qt.Timing == subscription.AtPeriodEnd {
			if _, err := q.InsertSubscriptionChange(ctx, sqlc.InsertSubscriptionChangeParams{
				ID: idgen.NewUUIDv7(), SubscriptionID: id, PlanVersionID: pc.NewPV.ID, Interval: string(pc.NewInterval), EffectiveAt: qt.Effective}); err != nil {
				return err
			}
			resp.Scheduled = true
			resp.Subscription = convert.SubscriptionOf(row, c.now())
			if err := subsqry.AttachExtras(ctx, q, resp.Subscription); err != nil {
				return err
			}
			return webhookscmd.EmitSubscription(ctx, q, c.now(), webhooksmodel.SubscriptionChangeScheduled, resp.Subscription, map[string]any{"quote": json.RawMessage(quoteJSON(resp.Quote))})
		}
		state := subscription.Active
		if pc.Trial {
			state = subscription.Trialing
		}
		feeDue := qt.NewFeeDue
		nr, err := subscriptionscmd.SwitchTo(ctx, q, c.now(), ti.ID, row, subscriptionscmd.Successor{PlanVersionID: pc.NewPV.ID, Interval: pc.NewInterval, State: state, TrialEnds: row.TrialEndsAt, At: pc.At,
			Anchor: qt.NewAnchor, FeeTiming: subscription.FeeInAdvance, FeeDue: &feeDue})
		if err != nil {
			return err
		}
		if qt.CreditMinor > 0 || qt.ChargeMinor > 0 {
			if err := q.InsertSubscriptionAdjustment(ctx, sqlc.InsertSubscriptionAdjustmentParams{
				ID: idgen.NewUUIDv7(), SubscriptionID: nr.ID, OldPlanCode: row.PlanCode, FromAt: pc.At, ToAt: qt.PeriodEnd, CreditMinor: qt.CreditMinor, ChargeMinor: qt.ChargeMinor}); err != nil {
				return err
			}
		}
		full, err := q.GetSubscription(ctx, nr.ID)
		if err != nil {
			return err
		}
		resp.Subscription = convert.SubscriptionOf(full, c.now())
		if err := subsqry.AttachExtras(ctx, q, resp.Subscription); err != nil {
			return err
		}
		return webhookscmd.EmitSubscription(ctx, q, c.now(), webhooksmodel.SubscriptionChanged, resp.Subscription, map[string]any{"previous_subscription_id": row.ID.String(), "quote": json.RawMessage(quoteJSON(resp.Quote))})
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return resp, nil
}

// CancelScheduled withdraws the change waiting for the end of the period.
func (c *Commands) CancelScheduled(ctx context.Context, req *plansv1.CancelScheduledChangeRequest) (*plansv1.Subscription, error) {
	id, err := domain.ParseID("subscription_id", req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.GetSubscriptionForUpdate(ctx, id); err != nil {
			return err
		}
		pending, err := q.GetPendingChange(ctx, id)
		if err != nil {
			return errkey.New(codes.FailedPrecondition, "plans.change.none_scheduled", "no change is scheduled")
		}
		if _, err := q.SettleChange(ctx, sqlc.SettleChangeParams{ID: pending.ID, State: "cancelled"}); err != nil {
			return err
		}
		r, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = convert.SubscriptionOf(r, c.now())
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

// ApplyScheduled applies the changes whose time has come. A change whose subscription is no longer able to change (ended, paused,
// past due, ending) is cancelled instead: the customer's situation moved on. Applying twice does nothing.
func (c *Commands) ApplyScheduled(ctx context.Context, req *plansv1.ApplyScheduledChangesRequest) (int32, error) {
	if req.GetNow() == nil || req.GetNow().CheckValid() != nil {
		return 0, domain.Invalid("now", "required")
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return 0, err
	}
	applied := int32(0)
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		applied = 0
		due, err := q.ListDueChanges(ctx, req.GetNow().AsTime())
		if err != nil {
			return err
		}
		for _, dc := range due {
			cur, err := q.GetSubscriptionForUpdate(ctx, dc.SubscriptionID)
			if err != nil {
				return err
			}
			row := sqlc.GetSubscriptionRow(cur)
			eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, req.GetNow().AsTime())
			if !row.OpenEnded || row.CancelAtPeriodEnd || eff != subscription.Active {
				if _, err := q.SettleChange(ctx, sqlc.SettleChangeParams{ID: dc.ID, State: "cancelled"}); err != nil {
					return err
				}
				continue
			}
			pv, err := q.GetPlanVersionByID(ctx, dc.PlanVersionID)
			if err != nil {
				return err
			}
			effective := dc.EffectiveAt
			nr, err := subscriptionscmd.SwitchTo(ctx, q, c.now(), ti.ID, row, subscriptionscmd.Successor{PlanVersionID: pv.ID, Interval: subscription.Interval(dc.Interval), State: subscription.Active,
				At: effective, Anchor: effective, FeeTiming: subscription.FeeInAdvance, FeeDue: &effective})
			if err != nil {
				return err
			}
			if _, err := q.SettleChange(ctx, sqlc.SettleChangeParams{ID: dc.ID, State: "applied", NewSubscriptionID: &nr.ID}); err != nil {
				return err
			}
			applied++
			full, err := q.GetSubscription(ctx, nr.ID)
			if err != nil {
				return err
			}
			msg := convert.SubscriptionOf(full, c.now())
			if err := subsqry.AttachExtras(ctx, q, msg); err != nil {
				return err
			}
			if err := webhookscmd.EmitSubscription(ctx, q, c.now(), webhooksmodel.SubscriptionChanged, msg, map[string]any{"previous_subscription_id": row.ID.String(), "scheduled": true}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, convert.Err(err, "subscription")
	}
	return applied, nil
}

// MarkAdjustmentBilled records that billing-svc has dealt with an adjustment. Marking twice is fine.
func (c *Commands) MarkAdjustmentBilled(ctx context.Context, adjustmentID string) error {
	id, err := domain.ParseID("adjustment_id", adjustmentID)
	if err != nil {
		return err
	}
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		_, err := q.MarkAdjustmentBilled(ctx, id)
		return err
	})
	if err != nil {
		return convert.Err(err, "adjustment")
	}
	return nil
}

// quoteJSON is a change quote the way the API returns it, for the event that announces the change.
func quoteJSON(q *plansv1.ChangeQuote) []byte {
	b, err := webhooksmodel.EventJSON.Marshal(q)
	if err != nil {
		return []byte("null")
	}
	return b
}
