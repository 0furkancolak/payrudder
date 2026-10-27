package rating_test

import (
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
)

func TestFixedFeeLines(t *testing.T) {
	start := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC) // 30 days
	mid := start.Add(15 * 24 * time.Hour)

	t.Run("one plan all month is the plain base fee", func(t *testing.T) {
		lines, err := rating.FixedFeeLines([]rating.FeeSegment{{PlanCode: "basic", FeeMinor: 3000, From: start, To: end}}, start, end)
		if err != nil || len(lines) != 1 || lines[0].AmountMinor != 3000 || lines[0].Description != "Base fee" || lines[0].Kind != rating.KindBaseFee {
			t.Fatalf("lines = %+v, %v", lines, err)
		}
	})
	t.Run("a plan change splits the month", func(t *testing.T) {
		lines, err := rating.FixedFeeLines([]rating.FeeSegment{
			{PlanCode: "basic", FeeMinor: 3000, From: start, To: mid},
			{PlanCode: "pro", FeeMinor: 9000, From: mid, To: end},
		}, start, end)
		if err != nil || len(lines) != 2 || lines[0].AmountMinor != 1500 || lines[1].AmountMinor != 4500 {
			t.Fatalf("lines = %+v, %v", lines, err)
		}
		if lines[0].Description != "Base fee — basic — 2027-01-01 to 2027-01-16" || lines[1].Description != "Base fee — pro — 2027-01-16 to 2027-01-31" {
			t.Fatalf("descriptions = %q, %q", lines[0].Description, lines[1].Description)
		}
	})
	t.Run("waived and free segments have no line", func(t *testing.T) {
		lines, err := rating.FixedFeeLines([]rating.FeeSegment{
			{PlanCode: "trial", FeeMinor: 3000, From: start, To: mid, Waived: true},
			{PlanCode: "free", FeeMinor: 0, From: mid, To: end},
		}, start, end)
		if err != nil || len(lines) != 0 {
			t.Fatalf("lines = %+v, %v", lines, err)
		}
	})
	t.Run("a segment outside the period is an error", func(t *testing.T) {
		if _, err := rating.FixedFeeLines([]rating.FeeSegment{{FeeMinor: 1, From: start.Add(-time.Hour), To: end}}, start, end); err == nil {
			t.Fatal("want an error")
		}
	})
}

// TaxOn rounds half to even, as TaxLine does: the credit that gives a taxed charge back must come to the same minor unit.
func TestTaxOn(t *testing.T) {
	for _, c := range []struct {
		amount int64
		bps    int32
		want   int64
	}{
		{500, 2000, 100},
		{224, 2000, 45}, // 44.8
		{203, 2000, 41}, // 40.6
		{5, 1000, 0},    // 0.5 goes to the even neighbour
		{15, 1000, 2},   // 1.5 too
		{1000, 0, 0},
	} {
		if got := rating.TaxOn(c.amount, c.bps); got != c.want {
			t.Errorf("TaxOn(%d, %d) = %d, want %d", c.amount, c.bps, got, c.want)
		}
	}
}
