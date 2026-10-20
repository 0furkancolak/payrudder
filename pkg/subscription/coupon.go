package subscription

// Coupon durations.
const (
	CouponOnce      = "once"
	CouponRepeating = "repeating"
	CouponForever   = "forever"
)

// CouponTerms is what a coupon takes off a fee.
type CouponTerms struct {
	// PercentBPS is a percentage in basis points (1000 = 10 %); zero for an amount coupon.
	PercentBPS int32
	// AmountMinor is a fixed amount in Currency; zero for a percentage coupon.
	AmountMinor int64
	Currency    string
	// Duration is CouponOnce, CouponRepeating (DurationPeriods fees) or CouponForever.
	Duration        string
	DurationPeriods int32
}

// CouponDiscount returns what the coupon takes off the fee of the next period of a subscription in currency that has had the coupon on
// periodsApplied fees before: a percentage of the fee rounded half-even to a minor unit, or the amount, never more than the fee. It is 0 when the
// coupon no longer applies (its duration is used up, or an amount coupon is in another currency) or the fee is nothing.
func CouponDiscount(fee int64, c CouponTerms, currency string, periodsApplied int32) int64 {
	if fee <= 0 || periodsApplied < 0 {
		return 0
	}
	switch c.Duration {
	case CouponOnce:
		if periodsApplied >= 1 {
			return 0
		}
	case CouponRepeating:
		if periodsApplied >= c.DurationPeriods {
			return 0
		}
	case CouponForever:
	default:
		return 0
	}
	if c.PercentBPS > 0 {
		return min(ScaleHalfEven(fee, int64(c.PercentBPS), 10000), fee)
	}
	if c.AmountMinor > 0 && c.Currency == currency {
		return min(c.AmountMinor, fee)
	}
	return 0
}
