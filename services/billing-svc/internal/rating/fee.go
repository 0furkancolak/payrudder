package rating

import (
	"fmt"
	"time"

	"github.com/0furkancolak/payrudder/pkg/subscription"
)

// FeeSegment is the part of a billing period one plan version was in force for a customer.
type FeeSegment struct {
	PlanCode string
	FeeMinor int64
	From, To time.Time
	// Waived segments (a trial that has not ended, a paused subscription) cost nothing.
	Waived bool
}

// FixedFeeLines prices the fixed fee of the period [periodStart, periodEnd) as one BASE_FEE line per segment, each the
// plan's fee times its share of the period (subscription.Portion). A single segment covering the whole period is the plain
// "Base fee" line, so a customer who never changed plan sees what they always saw. Segments that cost nothing yield no line.
func FixedFeeLines(segs []FeeSegment, periodStart, periodEnd time.Time) ([]Line, error) {
	var lines []Line
	for _, s := range segs {
		if s.Waived || s.FeeMinor == 0 {
			continue
		}
		amount, err := subscription.Portion(s.FeeMinor, s.From, s.To, periodStart, periodEnd)
		if err != nil {
			return nil, err
		}
		if amount == 0 {
			continue
		}
		desc := "Base fee"
		if len(segs) > 1 {
			desc = fmt.Sprintf("Base fee — %s — %s to %s", s.PlanCode, s.From.Format("2006-01-02"), s.To.Format("2006-01-02"))
		}
		lines = append(lines, Line{Kind: KindBaseFee, AmountMinor: amount, Description: desc, ServiceFrom: s.From, ServiceTo: s.To})
	}
	return lines, nil
}

// TaxLine returns the TAX line for lines at rateBps (2000 is 20 %), rounded half-even to a minor unit, exclusive of the price. The base is everything
// except a credit balance applied and tax itself: a discount lowers the base, a credit does not (it pays the invoice, it does not change what was sold).
// ok is false when there is nothing to tax.
func TaxLine(lines []Line, name string, rateBps int32) (Line, bool) {
	var base int64
	for _, l := range lines {
		if l.Kind != KindCredit && l.Kind != KindTax {
			base += l.AmountMinor
		}
	}
	if base <= 0 || rateBps <= 0 {
		return Line{}, false
	}
	amount := subscription.ScaleHalfEven(base, int64(rateBps), 10000)
	if amount == 0 {
		return Line{}, false
	}
	return Line{Kind: KindTax, AmountMinor: amount, Description: fmt.Sprintf("%s %s", name, percent(rateBps))}, true
}

// percent renders basis points as a percentage: 2000 → "20%", 750 → "7.5%", 825 → "8.25%".
func percent(bps int32) string {
	whole, frac := bps/100, bps%100
	switch {
	case frac == 0:
		return fmt.Sprintf("%d%%", whole)
	case frac%10 == 0:
		return fmt.Sprintf("%d.%d%%", whole, frac/10)
	}
	return fmt.Sprintf("%d.%02d%%", whole, frac)
}
