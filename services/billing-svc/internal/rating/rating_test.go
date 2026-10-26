package rating

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"pgregory.net/rapid"
)

func dec(t testing.TB, s string) *apd.Decimal {
	t.Helper()
	d, _, err := apd.NewFromString(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func perUnit(t testing.TB, price string) Price {
	return Price{MeterCode: "m", Model: PerUnit, UnitPrice: dec(t, price)}
}

func tiered(t testing.TB) Price {
	// 0–10 free, 10–100 at 2, above at 1.5; 100 flat fee when the second tier is entered, 7 when the third is.
	return Price{MeterCode: "m", Model: TieredGraduated, Tiers: []Tier{
		{UpTo: dec(t, "10"), UnitPrice: dec(t, "0")},
		{UpTo: dec(t, "100"), UnitPrice: dec(t, "2"), FlatFeeMinor: 100},
		{UnitPrice: dec(t, "1.5"), FlatFeeMinor: 7},
	}}
}

func TestAmount(t *testing.T) {
	tests := []struct {
		name  string
		price Price
		qty   string
		want  int64
	}{
		{"per unit fractional rounds half-even down", perUnit(t, "12.5"), "1.2345", 15},
		{"per unit 0.5 rounds to even 0", perUnit(t, "0.5"), "1", 0},
		{"per unit 1.5 rounds to even 2", perUnit(t, "1.5"), "1", 2},
		{"per unit 2.5 rounds to even 2", perUnit(t, "2.5"), "1", 2},
		{"per unit sub-minor price", perUnit(t, "0.002"), "1500", 3},
		{"per unit zero", perUnit(t, "9"), "0", 0},
		{"tiered inside the free tier", tiered(t), "10", 0},
		{"tiered second tier", tiered(t), "50", 40*2 + 100},
		{"tiered at the second tier top", tiered(t), "100", 90*2 + 100},
		{"tiered third tier with fractional qty", tiered(t), "120.5", 90*2 + 100 + 7 + 31}, // 20.5 × 1.5 = 30.75 → exact sum 317.75 → 318
		{"package rounds up", Price{Model: Package, PackageSize: dec(t, "100"), PackagePriceMinor: 500}, "101", 1000},
		{"package exact", Price{Model: Package, PackageSize: dec(t, "100"), PackagePriceMinor: 500}, "100", 500},
		{"package zero", Price{Model: Package, PackageSize: dec(t, "100"), PackagePriceMinor: 500}, "0", 0},
	}
	for _, tc := range tests {
		got, err := Amount(tc.price, dec(t, tc.qty))
		if err != nil || got != tc.want {
			t.Errorf("%s: Amount(%s) = %d, %v; want %d", tc.name, tc.qty, got, err, tc.want)
		}
	}
}

func TestIncrementalHonoursTiers(t *testing.T) {
	// 95 used in the period, 10 more arrive late: 5 of them in tier 2 (2 each), 5 in tier 3 (1.5 each, + flat 7).
	got, err := Incremental(tiered(t), dec(t, "95"), dec(t, "10"))
	// hi = 190+100+ (5×1.5=7.5 +7) → 90*2+100+7.5+7 = 294.5 → 294 (half-even), lo = 85*2+100 = 270.
	if err != nil || got != 294-270 {
		t.Fatalf("Incremental = %d, %v; want %d", got, err, 294-270)
	}
	alone, _ := Amount(tiered(t), dec(t, "10"))
	if got == alone {
		t.Fatalf("late usage priced alone (%d) equals the incremental price: tiers were ignored", alone)
	}
}

func TestRate(t *testing.T) {
	pv := PlanVersion{ID: "pv", BaseFeeMinor: 9900, Prices: map[string]Price{"gb": perUnit(t, "12.5"), "free": perUnit(t, "0")}}
	lines, err := Rate(pv, []Usage{
		{MeterCode: "unpriced", Quantity: dec(t, "5")},
		{MeterCode: "gb", Quantity: dec(t, "1.250"), UnitLabel: "GB-h"},
		{MeterCode: "free", Quantity: dec(t, "3"), UnitLabel: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0].Kind != KindBaseFee || lines[1].MeterCode != "free" || lines[2].MeterCode != "gb" {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[2].AmountMinor != 16 { // 1.25 × 12.5 = 15.625 → 16
		t.Fatalf("gb line = %d, want 16", lines[2].AmountMinor)
	}
	if total, _ := Total(lines); total != 9916 {
		t.Fatalf("total = %d, want 9916", total)
	}
	if lines[2].UnitPrice != "12.5" || lines[2].Quantity.Text('f') != "1.250" {
		t.Fatalf("display fields: %+v", lines[2])
	}
}

// More usage never costs less, and a late batch never has a negative incremental price.
func TestProperties(t *testing.T) {
	prices := []Price{perUnit(t, "0.37"), tiered(t), {Model: Package, PackageSize: dec(t, "7.5"), PackagePriceMinor: 31}}
	rapid.Check(t, func(r *rapid.T) {
		p := rapid.SampledFrom(prices).Draw(r, "price")
		a := rapid.Int64Range(0, 5_000_000).Draw(r, "a")
		b := rapid.Int64Range(0, 5_000_000).Draw(r, "b")
		qa, qb := apd.New(a, -3), apd.New(a+b, -3)
		lo, err1 := Amount(p, qa)
		hi, err2 := Amount(p, qb)
		if err1 != nil || err2 != nil {
			r.Fatalf("amount errors: %v %v", err1, err2)
		}
		if hi < lo {
			r.Fatalf("price fell from %d (qty %s) to %d (qty %s)", lo, qa, hi, qb)
		}
		inc, err := Incremental(p, qa, apd.New(b, -3))
		if err != nil || inc < 0 || inc != hi-lo {
			r.Fatalf("incremental = %d, %v; want %d", inc, err, hi-lo)
		}
	})
}

func TestNegativeQuantityRejected(t *testing.T) {
	if _, err := Amount(perUnit(t, "1"), dec(t, "-1")); err == nil {
		t.Fatal("negative quantity must be rejected")
	}
}

func TestIncludedQuantityIsFree(t *testing.T) {
	withIncluded := func(p Price, inc string) Price {
		p.Included = dec(t, inc)
		return p
	}
	tests := []struct {
		name  string
		price Price
		qty   string
		want  int64
	}{
		{"per unit under the included quantity", withIncluded(perUnit(t, "2"), "1000"), "999", 0},
		{"per unit at the included quantity", withIncluded(perUnit(t, "2"), "1000"), "1000", 0},
		{"per unit above it prices only the rest", withIncluded(perUnit(t, "2"), "1000"), "1250", 500},
		{"fractional included", withIncluded(perUnit(t, "10"), "0.5"), "1.25", 8}, // 0.75 × 10 = 7.5 → 8 (half-even)
		{"tiered starts its tiers above the included quantity", withIncluded(tiered(t), "100"), "150", 40*2 + 100},
		{"package counts packages above it", withIncluded(Price{Model: Package, PackageSize: dec(t, "100"), PackagePriceMinor: 500}, "50"), "151", 1000},
		{"zero included is none", withIncluded(perUnit(t, "2"), "0"), "3", 6},
	}
	for _, tc := range tests {
		got, err := Amount(tc.price, dec(t, tc.qty))
		if err != nil || got != tc.want {
			t.Errorf("%s: Amount(%s) = %d, %v; want %d", tc.name, tc.qty, got, err, tc.want)
		}
	}
	// Late usage adds what crossing the included quantity costs, not the price of the late quantity alone.
	p := withIncluded(perUnit(t, "2"), "1000")
	if got, err := Incremental(p, dec(t, "900"), dec(t, "300")); err != nil || got != 400 {
		t.Fatalf("Incremental across the included quantity = %d, %v; want 400", got, err)
	}
	// The line names what was free and drops the unit price: quantity × unit price is not its amount.
	lines, err := RateUsage(PlanVersion{Prices: map[string]Price{"calls": p}}, []Usage{{MeterCode: "calls", Quantity: dec(t, "1250"), UnitLabel: "calls"}})
	if err != nil || len(lines) != 1 || lines[0].AmountMinor != 500 || lines[0].UnitPrice != "" || lines[0].Description != "Usage — calls — 1250 calls (1000 calls included)" {
		t.Fatalf("lines = %+v, %v", lines, err)
	}
}
