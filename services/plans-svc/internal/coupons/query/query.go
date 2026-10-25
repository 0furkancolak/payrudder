// Package query reads coupons and works out what a coupon takes off a fee.
package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Queries reads coupons.
type Queries struct{ db *store.DB }

// New returns the coupon queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

var durationFromDB = map[string]plansv1.CouponDuration{
	subscription.CouponOnce:      plansv1.CouponDuration_COUPON_DURATION_ONCE,
	subscription.CouponRepeating: plansv1.CouponDuration_COUPON_DURATION_REPEATING,
	subscription.CouponForever:   plansv1.CouponDuration_COUPON_DURATION_FOREVER,
}

// Message maps a coupon row to its API message.
func Message(r sqlc.GetCouponRow) *plansv1.Coupon {
	c := &plansv1.Coupon{
		Code: r.Code, Name: r.Name, Duration: durationFromDB[r.Duration], Redeemed: r.Redeemed, Active: r.Active, CreateTime: timestamppb.New(r.CreatedAt),
	}
	if r.PercentBps != nil {
		c.PercentBps = *r.PercentBps
	}
	if r.AmountMinor != nil {
		c.AmountMinor = *r.AmountMinor
	}
	if r.Currency != nil {
		c.Currency = *r.Currency
	}
	if r.DurationPeriods != nil {
		c.DurationPeriods = *r.DurationPeriods
	}
	if r.MaxRedemptions != nil {
		c.MaxRedemptions = *r.MaxRedemptions
	}
	if r.ExpiresAt != nil {
		c.ExpireTime = timestamppb.New(*r.ExpiresAt)
	}
	return c
}

// List returns the tenant's coupons.
func (r *Queries) List(ctx context.Context) ([]*plansv1.Coupon, error) {
	var out []*plansv1.Coupon
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListCoupons(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			out = append(out, Message(sqlc.GetCouponRow(row)))
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "coupon")
	}
	return out, nil
}

// Discount is what the subscription's coupon takes off a fee of this period, and the coupon's code; 0 when none applies.
func Discount(ctx context.Context, q *sqlc.Queries, subID uuid.UUID, currency string, fee int64) (int64, string, error) {
	c, err := q.GetSubscriptionCoupon(ctx, subID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, "", nil
		}
		return 0, "", err
	}
	return subscription.CouponDiscount(fee, termsOf(c), currency, c.PeriodsApplied), c.CouponCode, nil
}

func termsOf(c sqlc.GetSubscriptionCouponRow) subscription.CouponTerms {
	terms := subscription.CouponTerms{Duration: c.Duration}
	if c.PercentBps != nil {
		terms.PercentBPS = *c.PercentBps
	}
	if c.AmountMinor != nil {
		terms.AmountMinor = *c.AmountMinor
	}
	if c.Currency != nil {
		terms.Currency = *c.Currency
	}
	if c.DurationPeriods != nil {
		terms.DurationPeriods = *c.DurationPeriods
	}
	return terms
}

// InForce returns what the subscription's coupon took off the fee of the period that is in force, which is already invoiced and so already counted in the
// coupon's periods (Discount is for the next one). known is false when the counter cannot say whether the period was discounted: a coupon that has used all
// its periods may have been used up by this one or earlier.
func InForce(ctx context.Context, q *sqlc.Queries, subID uuid.UUID, currency string) (discount func(fee int64) int64, known bool, err error) {
	none := func(int64) int64 { return 0 }
	c, err := q.GetSubscriptionCoupon(ctx, subID)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	terms := termsOf(c)
	// A probe fee: whether the coupon still has periods to give does not depend on the amount.
	live := subscription.CouponDiscount(1_000_000, terms, currency, c.PeriodsApplied) > 0
	switch {
	case c.PeriodsApplied == 0:
		return none, true, nil // the period in force was invoiced before the coupon had been used
	case !live:
		return nil, false, nil
	}
	return func(fee int64) int64 { return subscription.CouponDiscount(fee, terms, currency, c.PeriodsApplied-1) }, true, nil
}
