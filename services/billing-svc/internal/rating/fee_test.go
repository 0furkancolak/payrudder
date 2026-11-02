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
	t.Run("a single segment that starts after the period does says so", func(t *testing.T) {
		// A trial that ended on the 16th: the fee of the second half only, and the line names the days it covers.
		lines, err := rating.FixedFeeLines([]rating.FeeSegment{{PlanCode: "basic", FeeMinor: 3000, From: mid, To: end}}, start, end)
		if err != nil || len(lines) != 1 || lines[0].AmountMinor != 1500 || lines[0].Description != "Base fee — basic — 2027-01-16 to 2027-01-31" || !lines[0].ServiceFrom.Equal(mid) {
			t.Fatalf("lines = %+v, %v", lines, err)
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

// NetTaxLine is TaxLine that does not stop at zero: lines that come to less than nothing (late-usage corrections that outweigh the rest) give the tax of what is taken back
// back as a negative TAX line, rounded half to even like a positive one (so +5 and -5 at 10 % are both 0, +15 and -15 are +2 and -2). The result is continuous across zero.
func TestNetTaxLineGivesTheTaxBackOnANegativeBase(t *testing.T) {
	line := func(kind rating.Kind, amount int64) rating.Line { return rating.Line{Kind: kind, AmountMinor: amount} }
	for _, c := range []struct {
		name  string
		lines []rating.Line
		bps   int32
		want  int64
		ok    bool
	}{
		{"positive", []rating.Line{line(rating.KindBaseFee, 1000)}, 2000, 200, true},
		{"negative", []rating.Line{line(rating.KindBaseFee, 1000), line(rating.KindLateUsage, -2500)}, 2000, -300, true},
		{"the credit balance is no part of the base", []rating.Line{line(rating.KindLateUsage, -500), line(rating.KindCredit, -900)}, 2000, -100, true},
		{"half to even on the negative side", []rating.Line{line(rating.KindLateUsage, -15)}, 1000, -2, true},
		{"half to even, down", []rating.Line{line(rating.KindLateUsage, -5)}, 1000, 0, false},
		{"nothing", []rating.Line{line(rating.KindBaseFee, 100), line(rating.KindLateUsage, -100)}, 2000, 0, false},
		{"no rate", []rating.Line{line(rating.KindLateUsage, -500)}, 0, 0, false},
	} {
		got, ok := rating.NetTaxLine(c.lines, "VAT", c.bps)
		if ok != c.ok || got.AmountMinor != c.want || (ok && got.Kind != rating.KindTax) {
			t.Errorf("%s: NetTaxLine = %+v, %v; want %d, %v", c.name, got, ok, c.want, c.ok)
		}
	}
	// TaxLine itself is unchanged: nothing on a base that is not positive.
	if l, ok := rating.TaxLine([]rating.Line{line(rating.KindLateUsage, -2500)}, "VAT", 2000); ok {
		t.Errorf("TaxLine on a negative base = %+v", l)
	}
}
