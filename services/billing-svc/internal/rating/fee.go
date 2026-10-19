package rating

import (
	"fmt"
	"time"

	"github.com/0furkancolak/quotify/pkg/subscription"
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
		lines = append(lines, Line{Kind: KindBaseFee, AmountMinor: amount, Description: desc})
	}
	return lines, nil
}
