package rating

import (
	"fmt"
	"time"

	"github.com/0furkancolak/payrudder/pkg/money"
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
		if len(segs) > 1 || s.From.After(periodStart) || s.To.Before(periodEnd) { // part of the period: say which part
			desc = fmt.Sprintf("Base fee — %s — %s to %s", s.PlanCode, s.From.Format("2006-01-02"), s.To.Format("2006-01-02"))
		}
		lines = append(lines, Line{Kind: KindBaseFee, AmountMinor: amount, Description: desc, ServiceFrom: s.From, ServiceTo: s.To})
	}
	return lines, nil
}

// TaxOn is the tax at rateBps (2000 is 20 %) on an amount that does not include it, rounded half-even to a minor unit.
func TaxOn(amountMinor int64, rateBps int32) int64 {
	return subscription.ScaleHalfEven(amountMinor, int64(rateBps), 10000)
}

// TaxLine returns the TAX line for lines at rateBps (2000 is 20 %), rounded half-even to a minor unit, exclusive of the price. The base is everything
// except a credit balance applied and tax itself: a discount lowers the base, a credit does not (it pays the invoice, it does not change what was sold).
// ok is false when there is nothing to tax.
func TaxLine(lines []Line, name string, rateBps int32) (Line, bool) {
	if base := taxBase(lines); base > 0 {
		return taxLine(base, name, rateBps)
	}
	return Line{}, false
}

// NetTaxLine is TaxLine for the invoice of a billing run, which does not stop at zero: lines that come to less than nothing (late-usage corrections larger than the
// rest of the invoice) give back the tax of what is taken back, a negative TAX line of the same size TaxLine would have made for the same amount. Tax is on the net of the
// invoice on both sides of zero, the way a credit that returns a taxed charge includes its tax (plan changes, re-rating).
func NetTaxLine(lines []Line, name string, rateBps int32) (Line, bool) {
	base := taxBase(lines)
	if base >= 0 {
		return TaxLine(lines, name, rateBps)
	}
	l, ok := taxLine(-base, name, rateBps)
	l.AmountMinor = -l.AmountMinor
	return l, ok
}

func taxBase(lines []Line) int64 {
	var base int64
	for _, l := range lines {
		if l.Kind != KindCredit && l.Kind != KindTax {
			base += l.AmountMinor
		}
	}
	return base
}

// taxLine is the TAX line for a positive base.
func taxLine(base int64, name string, rateBps int32) (Line, bool) {
	if rateBps <= 0 {
		return Line{}, false
	}
	amount := TaxOn(base, rateBps)
	if amount == 0 {
		return Line{}, false
	}
	return Line{Kind: KindTax, AmountMinor: amount, Description: fmt.Sprintf("%s %s%%", name, money.FormatBps(int64(rateBps), true))}, true
}
