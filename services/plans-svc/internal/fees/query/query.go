// Package query reads the fixed fees billing-svc has to invoice in advance.
package query

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	couponsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// Queries reads fees.
type Queries struct {
	db  *store.DB
	now func() time.Time
}

// New returns the fee queries.
func New(db *store.DB, now func() time.Time) *Queries { return &Queries{db: db, now: now} }

// PeriodStartingAt returns the end of the billing period of sub that starts at start, or an error when start is not a period boundary of
// the subscription (the anchor moved, or the caller sent a wrong instant).
func PeriodStartingAt(sub sqlc.GetSubscriptionRow, start time.Time, loc *time.Location) (time.Time, error) {
	iv := subscription.Interval(sub.Interval)
	n, err := subscription.PeriodIndex(sub.BillingAnchor, iv, start, loc)
	if err != nil {
		return time.Time{}, errkey.New(codes.FailedPrecondition, "plans.fee.invalid_period", err.Error())
	}
	ps, err := subscription.PeriodStart(sub.BillingAnchor, iv, n, loc)
	if err != nil {
		return time.Time{}, err
	}
	if !ps.Equal(start) {
		return time.Time{}, errkey.Newf(codes.FailedPrecondition, "plans.fee.not_period_start", "%s is not the start of a billing period", start.Format(time.RFC3339))
	}
	return subscription.PeriodEnd(sub.BillingAnchor, iv, n, loc)
}

// Due is billing-svc's work list for fees in advance: every advance-billed, Payrudder-renewed subscription whose next period
// has started, with that period and its fee. A page is at most 500; resume with after.
func (r *Queries) Due(ctx context.Context, req *plansv1.ListSubscriptionsFeeDueRequest) ([]*plansv1.FeeDue, error) {
	if req.GetNow() == nil || req.GetNow().CheckValid() != nil {
		return nil, domain.Invalid("now", "required")
	}
	after := uuid.Nil
	if a := req.GetAfter(); a != "" {
		var err error
		if after, err = domain.ParseID("after", a); err != nil {
			return nil, err
		}
	}
	size := req.GetPageSize()
	if size <= 0 {
		size = 100
	}
	size = min(size, 500)
	var out []*plansv1.FeeDue
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		t, err := tenantsqry.Current(ctx, q)
		if err != nil {
			return err
		}
		loc, err := tenantsqry.Location(t.Timezone)
		if err != nil {
			return err
		}
		due, err := q.ListSubscriptionFeesDue(ctx, sqlc.ListSubscriptionFeesDueParams{Now: req.GetNow().AsTime(), After: after, PageSize: size})
		if err != nil {
			return err
		}
		for _, d := range due {
			row, err := q.GetSubscription(ctx, d.ID)
			if err != nil {
				return err
			}
			end, err := PeriodStartingAt(row, d.FeeDueAt, loc)
			if err != nil {
				return err
			}
			pv, err := q.GetPlanVersionByID(ctx, row.PlanVersionID)
			if err != nil {
				return err
			}
			fee, err := plansqry.FeeIn(ctx, q, pv.ID, pv.BaseFeeMinor, t.Currency, row.Currency, subscription.Interval(row.Interval))
			if err != nil {
				// A plan version always has the fee its subscription was created with; a gap here is data to look at, not a free period.
				return err
			}
			var discount int64
			var coupon string
			if row.State != string(subscription.Paused) {
				if discount, coupon, err = couponsqry.Discount(ctx, q, row.ID, row.Currency, fee); err != nil {
					return err
				}
			}
			out = append(out, &plansv1.FeeDue{
				Subscription: subsqry.Message(row, r.now()), PeriodStart: timestamppb.New(d.FeeDueAt), PeriodEnd: timestamppb.New(end), FeeMinor: fee,
				DiscountMinor: discount, CouponCode: coupon,
			})
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}
