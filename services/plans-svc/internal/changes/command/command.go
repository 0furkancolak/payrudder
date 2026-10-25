// Package command makes, schedules, withdraws and applies subscription changes, and records the money they move.
package command

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	changesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	entitlementscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/entitlements/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks"
)

// Commands changes subscriptions from one plan version or interval to another.
type Commands struct {
	db  *store.DB
	now func() time.Time
}

// New returns the change commands.
func New(db *store.DB, now func() time.Time) *Commands { return &Commands{db: db, now: now} }

// Successor is the subscription that takes over from another: at a plan or interval change, at a provider sync, at a provider
// cutover. The three share SwitchTo, so they end the old subscription, start the new one, carry the coupon and publish the
// entitlements by one rule.
type Successor struct {
	PlanVersionID uuid.UUID
	Interval      subscription.Interval
	State         subscription.State
	TrialEnds     *time.Time
	At, Anchor    time.Time
	// FeeTiming and FeeDue: "advance" with the start of the next period to invoice, or "arrears" with none.
	FeeTiming string
	FeeDue    *time.Time
	// Owner and ProviderRef carry a provider-run subscription's link over. Owner "" is Payrudder.
	Owner       string
	ProviderRef *string
	// Provider is the successor's payment provider; nil keeps the old subscription's.
	Provider *string
	// LeaveOldOpen ends the old subscription at At without marking it cancelled yet: a cutover dated in the future, where the
	// old one is still live until then.
	LeaveOldOpen bool
}

// ownerPayrudder is the Owner of a subscription Payrudder itself invoices (the stored value predates the rename).
const ownerPayrudder = "quotify"

// carriesCoupon is the one rule for the coupon of a subscription that is replaced: it follows the subscription, with the
// periods it has used, as long as the successor can hold one. A coupon takes something off a fee Payrudder invoices in
// advance (coupons.Redeem refuses any other subscription), so a successor that a provider runs, or that is billed in
// arrears, leaves it on the ended subscription.
func carriesCoupon(next Successor) bool {
	return (next.Owner == "" || next.Owner == ownerPayrudder) && next.FeeTiming == "advance"
}

// SwitchTo ends old at next.At and starts its successor in the same transaction, with the entitlements of both.
func SwitchTo(ctx context.Context, q *sqlc.Queries, now time.Time, tenantID uuid.UUID, old sqlc.GetSubscriptionRow, next Successor) (sqlc.InsertSubscriptionRow, error) {
	at := next.At
	if n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: old.ID, EndTime: at}); err != nil {
		return sqlc.InsertSubscriptionRow{}, err
	} else if n != 1 {
		return sqlc.InsertSubscriptionRow{}, errkey.New(codes.Aborted, "plans.subscription.changed_concurrently", "subscription changed concurrently; retry")
	}
	if !next.LeaveOldOpen {
		if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: old.ID, State: string(subscription.Cancelled), PausedAt: old.PausedAt}); err != nil {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	if next.ProviderRef != nil && old.ProviderRef != nil && *next.ProviderRef == *old.ProviderRef {
		if _, err := q.ReleaseProviderLink(ctx, old.ID); err != nil {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	oldID := old.ID
	owner := next.Owner
	if owner == "" {
		owner = ownerPayrudder
	}
	provider := old.Provider
	if next.Provider != nil {
		provider = next.Provider
	}
	nr, err := q.InsertSubscription(ctx, sqlc.InsertSubscriptionParams{
		ID: idgen.NewUUIDv7(), CustomerID: old.CustomerID, PlanVersionID: next.PlanVersionID, StartTime: at,
		State: string(next.State), Interval: string(next.Interval), Currency: old.Currency, TrialEndsAt: next.TrialEnds,
		BillingAnchor: next.Anchor, ChangedFromID: &oldID, Provider: provider, ProviderRef: next.ProviderRef, Owner: owner, FeeTiming: next.FeeTiming, FeeDueAt: next.FeeDue,
	})
	if err != nil {
		return nr, err
	}
	if carriesCoupon(next) {
		if _, err := q.MoveSubscriptionCoupon(ctx, sqlc.MoveSubscriptionCouponParams{OldID: old.ID, NewID: nr.ID}); err != nil {
			return nr, err
		}
	}
	// Old entitlements first, new ones last: the topic is compacted per customer and meter, so the last write wins.
	if err := entitlementscmd.Publish(ctx, q, tenantID, old.CustomerID, old.PlanVersionID, old.StartTime, &at, now); err != nil {
		return nr, err
	}
	return nr, entitlementscmd.Publish(ctx, q, tenantID, nr.CustomerID, nr.PlanVersionID, at, nil, now)
}

// Change moves a subscription billed in advance to another plan version and/or interval, now or at the end of the paid period.
func (c *Commands) Change(ctx context.Context, req *plansv1.ChangeSubscriptionRequest) (*plansv1.ChangeSubscriptionResponse, error) {
	id, err := subsqry.ParseID(req.GetSubscriptionId())
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
			resp.Subscription = subsqry.Message(row, c.now())
			if err := subsqry.AttachExtras(ctx, q, resp.Subscription); err != nil {
				return err
			}
			return webhooks.EmitSubscription(ctx, q, c.now(), webhooks.SubscriptionChangeScheduled, resp.Subscription, map[string]any{"quote": json.RawMessage(quoteJSON(resp.Quote))})
		}
		state := subscription.Active
		if pc.Trial {
			state = subscription.Trialing
		}
		feeDue := qt.NewFeeDue
		nr, err := SwitchTo(ctx, q, c.now(), ti.ID, row, Successor{PlanVersionID: pc.NewPV.ID, Interval: pc.NewInterval, State: state, TrialEnds: row.TrialEndsAt, At: pc.At,
			Anchor: qt.NewAnchor, FeeTiming: "advance", FeeDue: &feeDue})
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
		resp.Subscription = subsqry.Message(full, c.now())
		if err := subsqry.AttachExtras(ctx, q, resp.Subscription); err != nil {
			return err
		}
		return webhooks.EmitSubscription(ctx, q, c.now(), webhooks.SubscriptionChanged, resp.Subscription, map[string]any{"previous_subscription_id": row.ID.String(), "quote": json.RawMessage(quoteJSON(resp.Quote))})
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return resp, nil
}

// CancelScheduled withdraws the change waiting for the end of the period.
func (c *Commands) CancelScheduled(ctx context.Context, req *plansv1.CancelScheduledChangeRequest) (*plansv1.Subscription, error) {
	id, err := subsqry.ParseID(req.GetSubscriptionId())
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
		out = subsqry.Message(r, c.now())
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
			nr, err := SwitchTo(ctx, q, c.now(), ti.ID, row, Successor{PlanVersionID: pv.ID, Interval: subscription.Interval(dc.Interval), State: subscription.Active,
				At: effective, Anchor: effective, FeeTiming: "advance", FeeDue: &effective})
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
			msg := subsqry.Message(full, c.now())
			if err := subsqry.AttachExtras(ctx, q, msg); err != nil {
				return err
			}
			if err := webhooks.EmitSubscription(ctx, q, c.now(), webhooks.SubscriptionChanged, msg, map[string]any{"previous_subscription_id": row.ID.String(), "scheduled": true}); err != nil {
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
	id, err := uuid.Parse(adjustmentID)
	if err != nil {
		return domain.Invalid("adjustment_id", "must be a UUID")
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

// CreditUnusedFee records the unused prepaid fee as an adjustment on the subscription, which billing turns into credit balance.
func CreditUnusedFee(ctx context.Context, q *sqlc.Queries, now time.Time, row sqlc.GetSubscriptionRow, at time.Time) (int64, error) {
	credit, pe, ok, err := changesqry.UnusedFee(ctx, q, now, row, at)
	if err != nil || !ok {
		return 0, err
	}
	return credit, q.InsertSubscriptionAdjustment(ctx, sqlc.InsertSubscriptionAdjustmentParams{
		ID: idgen.NewUUIDv7(), SubscriptionID: row.ID, OldPlanCode: row.PlanCode, FromAt: at, ToAt: pe, CreditMinor: credit, ChargeMinor: 0})
}

// quoteJSON is a change quote the way the API returns it, for the event that announces the change.
func quoteJSON(q *plansv1.ChangeQuote) []byte {
	b, err := webhooks.EventJSON.Marshal(q)
	if err != nil {
		return []byte("null")
	}
	return b
}
