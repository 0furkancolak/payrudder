package grpcapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// periodStartingAt returns the end of the billing period of sub that starts at start, or an error when start is not a period boundary of
// the subscription (the anchor moved, or the caller sent a wrong instant).
func periodStartingAt(sub sqlc.GetSubscriptionRow, start time.Time, loc *time.Location) (time.Time, error) {
	iv := subscription.Interval(sub.Interval)
	n, err := subscription.PeriodIndex(sub.BillingAnchor, iv, start, loc)
	if err != nil {
		return time.Time{}, status.Error(codes.FailedPrecondition, err.Error())
	}
	ps, err := subscription.PeriodStart(sub.BillingAnchor, iv, n, loc)
	if err != nil {
		return time.Time{}, err
	}
	if !ps.Equal(start) {
		return time.Time{}, status.Errorf(codes.FailedPrecondition, "%s is not the start of a billing period", start.Format(time.RFC3339))
	}
	return subscription.PeriodEnd(sub.BillingAnchor, iv, n, loc)
}

// ListSubscriptionsFeeDue is billing-svc's work list for fees in advance: every advance-billed, Quotify-renewed subscription whose next period
// has started, with that period and its fee. A page is at most 500; resume with after.
func (s *Server) ListSubscriptionsFeeDue(ctx context.Context, req *plansv1.ListSubscriptionsFeeDueRequest) (*plansv1.ListSubscriptionsFeeDueResponse, error) {
	if req.GetNow() == nil || req.GetNow().CheckValid() != nil {
		return nil, domain.Invalid("now", "required")
	}
	after := uuid.Nil
	if a := req.GetAfter(); a != "" {
		var err error
		if after, err = uuid.Parse(a); err != nil {
			return nil, domain.Invalid("after", "must be a UUID")
		}
	}
	size := req.GetPageSize()
	if size <= 0 {
		size = 100
	}
	size = min(size, 500)
	resp := &plansv1.ListSubscriptionsFeeDueResponse{}
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		t, err := current(ctx, q)
		if err != nil {
			return err
		}
		loc, err := location(t.Timezone)
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
			end, err := periodStartingAt(row, d.FeeDueAt, loc)
			if err != nil {
				return err
			}
			pv, err := q.GetPlanVersionByID(ctx, row.PlanVersionID)
			if err != nil {
				return err
			}
			fee, err := feeIn(ctx, q, pv.ID, pv.BaseFeeMinor, t.Currency, row.Currency, subscription.Interval(row.Interval))
			if err != nil {
				// A plan version always has the fee its subscription was created with; a gap here is data to look at, not a free period.
				return err
			}
			var discount int64
			var coupon string
			if row.State != string(subscription.Paused) {
				if discount, coupon, err = couponDiscount(ctx, q, row.ID, row.Currency, fee); err != nil {
					return err
				}
			}
			resp.Due = append(resp.Due, &plansv1.FeeDue{
				Subscription: subscriptionMessage(row, s.now()), PeriodStart: timestamppb.New(d.FeeDueAt), PeriodEnd: timestamppb.New(end), FeeMinor: fee,
				DiscountMinor: discount, CouponCode: coupon,
			})
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return resp, nil
}

// AdvanceSubscriptionFee marks the fee period starting at period_start as dealt with by moving fee_due_at to the period's end. It is a
// compare-and-set: the same call twice, or a call for a period that is not the next one, changes nothing.
func (s *Server) AdvanceSubscriptionFee(ctx context.Context, req *plansv1.AdvanceSubscriptionFeeRequest) (*plansv1.AdvanceSubscriptionFeeResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if req.GetPeriodStart() == nil || req.GetPeriodStart().CheckValid() != nil {
		return nil, domain.Invalid("period_start", "required")
	}
	start := req.GetPeriodStart().AsTime()
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		t, err := current(ctx, q)
		if err != nil {
			return err
		}
		loc, err := location(t.Timezone)
		if err != nil {
			return err
		}
		row, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		end, err := periodStartingAt(sqlc.GetSubscriptionRow(row), start, loc)
		if err != nil {
			return err
		}
		n, err := q.AdvanceSubscriptionFee(ctx, sqlc.AdvanceSubscriptionFeeParams{ID: id, PeriodStart: start, NextDue: end})
		if err != nil || n == 0 {
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
		fee, err := feeIn(ctx, q, pv.ID, pv.BaseFeeMinor, t.Currency, row.Currency, subscription.Interval(row.Interval))
		if err != nil {
			return err
		}
		if discount, _, err := couponDiscount(ctx, q, id, row.Currency, fee); err != nil {
			return err
		} else if discount > 0 {
			return q.CountCouponPeriod(ctx, id)
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.AdvanceSubscriptionFeeResponse{}, nil
}
