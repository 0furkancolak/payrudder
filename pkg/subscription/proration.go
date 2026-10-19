package subscription

import (
	"errors"
	"math/big"
	"time"
)

// ErrBadPortion: the segment is not inside the period, the period is empty, or the fee is negative.
var ErrBadPortion = errors.New("subscription: portion needs 0 <= fee and period.start <= from <= to <= period.end with period.start < period.end")

// Portion prices the part of a period's fixed fee that falls in [from, to): fee times the segment's share of the period,
// rounded half-even to a minor unit once. A plan change inside a period splits the period into segments, each priced at
// its own plan's fee; a segment that covers the whole period costs the whole fee.
func Portion(fee int64, from, to, periodStart, periodEnd time.Time) (int64, error) {
	if fee < 0 || !periodStart.Before(periodEnd) || from.Before(periodStart) || to.After(periodEnd) || to.Before(from) {
		return 0, ErrBadPortion
	}
	return scaleHalfEven(fee, int64(to.Sub(from)), int64(periodEnd.Sub(periodStart))), nil
}

// scaleHalfEven returns round_half_even(v * num / den) using exact integer arithmetic.
func scaleHalfEven(v, num, den int64) int64 {
	n := new(big.Int).Mul(big.NewInt(v), big.NewInt(num))
	d := big.NewInt(den)
	q, r := new(big.Int).QuoRem(n, d, new(big.Int))
	twice := new(big.Int).Lsh(r, 1)
	switch twice.Cmp(d) {
	case 1:
		q.Add(q, big.NewInt(1))
	case 0:
		if q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
	}
	return q.Int64()
}
