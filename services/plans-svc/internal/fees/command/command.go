// Package command marks the fixed fee of a billing period as dealt with.
package command

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	couponscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/command"
	couponsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	feesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/fees/query"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subscriptionscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/command"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// Commands changes the fee state of subscriptions.
type Commands struct{ db *store.DB }

// New returns the fee commands.
func New(db *store.DB) *Commands { return &Commands{db: db} }

// Advance marks the fee period starting at period_start as dealt with by moving fee_due_at to the period's end. It is a
// compare-and-set: the same call twice, or a call for a period that is not the next one, changes nothing.
func (c *Commands) Advance(ctx context.Context, req *plansv1.AdvanceSubscriptionFeeRequest) error {
	id, err := domain.ParseID("subscription_id", req.GetSubscriptionId())
	if err != nil {
		return err
	}
	if req.GetPeriodStart() == nil || req.GetPeriodStart().CheckValid() != nil {
		return domain.Invalid("period_start", "required")
	}
	start := req.GetPeriodStart().AsTime()
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		t, loc, err := tenantsqry.CurrentIn(ctx, q)
		if err != nil {
			return err
		}
		row, err := subsqry.Lock(ctx, q, id)
		if err != nil {
			return err
		}
		end, err := feesqry.PeriodStartingAt(row, start, loc)
		if err != nil {
			return err
		}
		advanced, err := subscriptionscmd.AdvanceFee(ctx, q, id, start, end)
		if err != nil || !advanced {
			return err
		}
		// The period was dealt with just now (not by an earlier call): if the coupon took something off its fee, that used one of the coupon's periods.
		if row.State == string(subscription.Paused) {
			return nil
		}
		pv, err := q.GetPlanVersionByID(ctx, row.PlanVersionID)
		if err != nil {
			return err
		}
		fee, err := plansqry.FeeIn(ctx, q, pv.ID, pv.BaseFeeMinor, t.Currency, row.Currency, subscription.Interval(row.Interval))
		if err != nil {
			return err
		}
		if discount, _, err := couponsqry.Discount(ctx, q, id, row.Currency, fee); err != nil {
			return err
		} else if discount > 0 {
			return couponscmd.UsedOnFee(ctx, q, id, discount)
		}
		return nil
	})
	if err != nil {
		return convert.Err(err, "subscription")
	}
	return nil
}
