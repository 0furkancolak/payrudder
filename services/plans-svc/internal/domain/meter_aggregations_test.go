package domain

import (
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func TestValidateMeterUniqueAndLast(t *testing.T) {
	req := func(agg plansv1.Aggregation, prop string, scale uint32) *plansv1.CreateMeterRequest {
		return &plansv1.CreateMeterRequest{Code: "active_users", Aggregation: agg, ValueProperty: prop, QuantityScale: scale, UnitLabel: "users"}
	}
	unique, last := plansv1.Aggregation_AGGREGATION_UNIQUE, plansv1.Aggregation_AGGREGATION_LAST
	in, err := ValidateMeter(req(unique, "user_id", 0), -1, "")
	if err != nil || in.Aggregation != "UNIQUE" || *in.ValueProperty != "user_id" {
		t.Fatalf("a valid UNIQUE meter: %+v, %v", in, err)
	}
	if in, err = ValidateMeter(req(last, "seats", 2), -1, ""); err != nil || in.Aggregation != "LAST" || in.Scale != 2 {
		t.Fatalf("a valid LAST meter keeps its scale: %+v, %v", in, err)
	}
	for _, c := range []struct {
		name  string
		r     *plansv1.CreateMeterRequest
		scale int
		prev  string
		field string
	}{
		{"UNIQUE needs the property it counts", req(unique, "", 0), -1, "", "value_property"},
		{"UNIQUE counts whole values", req(unique, "user_id", 2), -1, "", "quantity_scale"},
		{"LAST needs a property like SUM", req(last, "", 0), -1, "", "value_property"},
		{"a SUM meter cannot become UNIQUE", req(unique, "user_id", 0), 0, "SUM", "aggregation"},
		{"a UNIQUE meter cannot become SUM", req(plansv1.Aggregation_AGGREGATION_SUM, "n", 0), 0, "UNIQUE", "aggregation"},
		{"a MAX meter cannot become LAST", req(last, "n", 0), 0, "MAX", "aggregation"},
		{"a LAST meter cannot become UNIQUE", req(unique, "user_id", 0), 0, "LAST", "aggregation"},
	} {
		if _, err := ValidateMeter(c.r, c.scale, c.prev); !contains(fields(t, err), c.field) {
			t.Errorf("%s: want a violation of %s, got %v", c.name, c.field, err)
		}
	}
	// Staying on UNIQUE or LAST is a new version like any other, and SUM, COUNT and MAX still switch among themselves.
	if _, err := ValidateMeter(req(unique, "account_id", 0), 0, "UNIQUE"); err != nil {
		t.Errorf("a new UNIQUE version: %v", err)
	}
	if _, err := ValidateMeter(req(plansv1.Aggregation_AGGREGATION_MAX, "n", 0), 0, "SUM"); err != nil {
		t.Errorf("SUM to MAX: %v", err)
	}
}

func TestAggregationNamesRoundTrip(t *testing.T) {
	for _, a := range []plansv1.Aggregation{
		plansv1.Aggregation_AGGREGATION_SUM, plansv1.Aggregation_AGGREGATION_COUNT, plansv1.Aggregation_AGGREGATION_MAX,
		plansv1.Aggregation_AGGREGATION_UNIQUE, plansv1.Aggregation_AGGREGATION_LAST,
	} {
		name, ok := AggregationToDB(a)
		if !ok || AggregationFromDB(name) != a {
			t.Errorf("%v -> %q, %v -> %v", a, name, ok, AggregationFromDB(name))
		}
	}
}
