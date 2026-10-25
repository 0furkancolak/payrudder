package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Move hands the coupon of a replaced subscription to its successor, with the periods it has used. A subscription without one
// is left alone.
func Move(ctx context.Context, q *sqlc.Queries, oldID, newID uuid.UUID) error {
	_, err := q.MoveSubscriptionCoupon(ctx, sqlc.MoveSubscriptionCouponParams{OldID: oldID, NewID: newID})
	return err
}

// UsedOnFee records that the coupon of a subscription took discount off one fee: the redemption's total grows and the coupon has
// used one more of its periods.
func UsedOnFee(ctx context.Context, q *sqlc.Queries, subscriptionID uuid.UUID, discount int64) error {
	if err := q.AddRedemptionDiscount(ctx, sqlc.AddRedemptionDiscountParams{SubscriptionID: subscriptionID, Discount: discount}); err != nil {
		return err
	}
	return q.CountCouponPeriod(ctx, subscriptionID)
}
