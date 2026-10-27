package rating

import "testing"

// Rating is a pure function of the quantity the aggregator stored, whatever the meter's aggregation: a UNIQUE meter's quantity is its number of distinct values
// and a LAST meter's is the value of its latest event, so both price like any other quantity (including an included allowance and tiers).
func TestUniqueAndLastPriceTheirStoredQuantity(t *testing.T) {
	// Five active users are included, each further one costs 100.
	users := PlanVersion{Prices: map[string]Price{"active_users": {MeterCode: "active_users", Model: PerUnit, UnitPrice: dec(t, "100"), Included: dec(t, "5")}}}
	lines, err := RateUsage(users, []Usage{{MeterCode: "active_users", Quantity: dec(t, "12"), UnitLabel: "users"}})
	if err != nil || len(lines) != 1 || lines[0].AmountMinor != 700 || lines[0].Kind != KindUsage {
		t.Fatalf("12 distinct users = %+v, %v; want one usage line of 700", lines, err)
	}
	// Seats are billed for the quantity at the end of the period: 8 seats, whatever the earlier events said, with graduated tiers.
	seats := PlanVersion{Prices: map[string]Price{"seats": {MeterCode: "seats", Model: TieredGraduated, Tiers: []Tier{
		{UpTo: dec(t, "5"), UnitPrice: dec(t, "10")}, {UnitPrice: dec(t, "8")},
	}}}}
	lines, err = RateUsage(seats, []Usage{{MeterCode: "seats", Quantity: dec(t, "8"), UnitLabel: "seats"}})
	if err != nil || len(lines) != 1 || lines[0].AmountMinor != 5*10+3*8 {
		t.Fatalf("8 seats = %+v, %v; want 74", lines, err)
	}
}

// Late usage of a UNIQUE meter is additive by construction (the aggregator bills a value as late in one period only and only if no on-time event carried it), so the
// incremental price of its late values honours the allowance and the tiers exactly like a SUM meter's, over the on-time count and the late values billed before.
func TestLateUniqueValuesArePricedIncrementally(t *testing.T) {
	users := PlanVersion{Prices: map[string]Price{"active_users": {MeterCode: "active_users", Model: PerUnit, UnitPrice: dec(t, "100"), Included: dec(t, "5")}}}
	// 4 users on time: inside the allowance. Three late values bring it to 7: two are paid for.
	line, ok, err := LateLine(users, "active_users", "users", "2026-10", dec(t, "4"), dec(t, "3"))
	if err != nil || !ok || line.AmountMinor != 200 || line.Kind != KindLateUsage || line.OriginPeriod != "2026-10" {
		t.Fatalf("late line = %+v, %v, %v; want 200", line, ok, err)
	}
	// The next invoice carries two more late values on top of the 4 + 3 billed so far: both are paid for.
	line, ok, err = LateLine(users, "active_users", "users", "2026-10", dec(t, "7"), dec(t, "2"))
	if err != nil || !ok || line.AmountMinor != 200 {
		t.Fatalf("second late line = %+v, %v, %v; want 200", line, ok, err)
	}
	// A value that was already counted on time never shows up as late usage, so there is nothing to bill.
	if _, ok, err := LateLine(users, "active_users", "users", "2026-10", dec(t, "7"), dec(t, "0")); err != nil || ok {
		t.Fatalf("zero late values produced a line: %v, %v", ok, err)
	}
}
