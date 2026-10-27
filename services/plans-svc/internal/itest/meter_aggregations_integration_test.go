//go:build integration

package itest

import (
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// UNIQUE and LAST meters are stored, read back with their aggregation and value property, and a code cannot change into or
// out of them in a later version, while SUM, COUNT and MAX can still switch among themselves.
func testMeterAggregations(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Aggregations")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	create := func(code string, agg plansv1.Aggregation, property string, scale uint32) (*plansv1.CreateMeterResponse, error) {
		return e.plans.CreateMeter(ctx, &plansv1.CreateMeterRequest{Code: code, Aggregation: agg, ValueProperty: property, QuantityScale: scale, UnitLabel: "u"})
	}
	unique, last := plansv1.Aggregation_AGGREGATION_UNIQUE, plansv1.Aggregation_AGGREGATION_LAST

	u, err := create("active_users", unique, "user_id", 0)
	if err != nil || u.GetMeter().GetAggregation() != unique || u.GetMeter().GetValueProperty() != "user_id" || u.GetMeter().GetVersion() != 1 {
		t.Fatalf("UNIQUE meter = %v, %v", u, err)
	}
	l, err := create("seats_now", last, "seats", 2)
	if err != nil || l.GetMeter().GetAggregation() != last || l.GetMeter().GetQuantityScale() != 2 {
		t.Fatalf("LAST meter = %v, %v", l, err)
	}
	got, err := e.plans.GetMeter(ctx, &plansv1.GetMeterRequest{Code: "active_users"})
	if err != nil || got.GetMeter().GetAggregation() != unique {
		t.Fatalf("GetMeter = %v, %v", got, err)
	}

	// The rules of the editor: a UNIQUE meter names its property and counts whole values.
	if _, err := create("no_property", unique, "", 0); code(t, err) != codes.InvalidArgument {
		t.Fatalf("UNIQUE without a property: %v", err)
	}
	if _, err := create("fractional", unique, "user_id", 1); code(t, err) != codes.InvalidArgument {
		t.Fatalf("UNIQUE with decimal places: %v", err)
	}

	// A new version of a UNIQUE code may name another property but stays UNIQUE.
	v2, err := create("active_users", unique, "account_id", 0)
	if err != nil || v2.GetMeter().GetVersion() != 2 || v2.GetMeter().GetValueProperty() != "account_id" {
		t.Fatalf("UNIQUE v2 = %v, %v", v2, err)
	}
	if _, err := create("active_users", plansv1.Aggregation_AGGREGATION_SUM, "n", 0); code(t, err) != codes.InvalidArgument {
		t.Fatalf("UNIQUE to SUM: %v", err)
	}
	if _, err := create("seats_now", unique, "seats", 0); code(t, err) != codes.InvalidArgument {
		t.Fatalf("LAST to UNIQUE: %v", err)
	}

	// SUM, COUNT and MAX keep switching among themselves, but cannot become LAST.
	if _, err := create("tokens", plansv1.Aggregation_AGGREGATION_SUM, "n", 0); err != nil {
		t.Fatal(err)
	}
	if v, err := create("tokens", plansv1.Aggregation_AGGREGATION_MAX, "n", 0); err != nil || v.GetMeter().GetVersion() != 2 {
		t.Fatalf("SUM to MAX: %v, %v", v, err)
	}
	if _, err := create("tokens", last, "n", 0); code(t, err) != codes.InvalidArgument {
		t.Fatalf("MAX to LAST: %v", err)
	}

	list, err := e.plans.ListMeters(ctx, &plansv1.ListMetersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]plansv1.Aggregation{}
	for _, m := range list.GetMeters() {
		seen[m.GetCode()] = m.GetAggregation()
	}
	if seen["active_users"] != unique || seen["seats_now"] != last || seen["tokens"] != plansv1.Aggregation_AGGREGATION_MAX {
		t.Fatalf("listed aggregations = %v", seen)
	}
}
