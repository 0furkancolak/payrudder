package domain

import (
	"strings"
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func perUnit(meter, price string) *plansv1.Price {
	return &plansv1.Price{MeterCode: meter, Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: price}}}
}

func TestValidatePlanVersionQuotaTerms(t *testing.T) {
	scales := map[string]int{"api_calls": 0, "gb_hours": 3, "seats": 0}
	calls := perUnit("api_calls", "0.01")
	calls.Included = "1000"
	in, err := ValidatePlanVersion(&plansv1.PublishPlanVersionRequest{
		Prices: []*plansv1.Price{calls},
		Entitlements: []*plansv1.Entitlement{
			{MeterCode: "api_calls", Limit: "5000", AlertThresholdsPct: []int32{50, 80, 100}, Window: plansv1.QuotaWindow_QUOTA_WINDOW_DAY, Mode: plansv1.QuotaMode_QUOTA_MODE_SOFT},
			{MeterCode: "gb_hours", Limit: "10", Window: plansv1.QuotaWindow_QUOTA_WINDOW_WEEK, Mode: plansv1.QuotaMode_QUOTA_MODE_NOTIFY},
			{MeterCode: "seats", Limit: "3"},
		},
	}, scales)
	if err != nil {
		t.Fatal(err)
	}
	want := []StoredEntitlement{
		{MeterCode: "api_calls", LimitUnits: 5000, Thresholds: []int32{50, 80, 100}, Window: "day", Mode: "soft"},
		{MeterCode: "gb_hours", LimitUnits: 10000, Thresholds: []int32{80, 100}, Window: "week", Mode: "notify"},
		// What a request without the new fields always meant: the billing month, hard.
		{MeterCode: "seats", LimitUnits: 3, Thresholds: []int32{80, 100}, Window: "month", Mode: "hard"},
	}
	for i, w := range want {
		g := in.Entitlements[i]
		if g.MeterCode != w.MeterCode || g.LimitUnits != w.LimitUnits || g.Window != w.Window || g.Mode != w.Mode || len(g.Thresholds) != len(w.Thresholds) {
			t.Errorf("entitlements[%d] = %+v, want %+v", i, g, w)
		}
	}
	if !strings.Contains(string(in.Prices[0].Config), `"included_units":1000`) {
		t.Fatalf("price config = %s", in.Prices[0].Config)
	}
	back, err := PriceFromStored("api_calls", in.Prices[0].Model, in.Prices[0].Config, 0)
	if err != nil || back.GetIncluded() != "1000" || back.GetPerUnit().GetUnitPrice() != "0.01" {
		t.Fatalf("round trip = %v, %v", back, err)
	}
	// A config stored before included quantities existed reads as none.
	old, err := PriceFromStored("api_calls", "PER_UNIT", []byte(`{"unit_price":"0.01"}`), 0)
	if err != nil || old.GetIncluded() != "" {
		t.Fatalf("old config = %v, %v", old, err)
	}
	if WindowFromDB("week") != plansv1.QuotaWindow_QUOTA_WINDOW_WEEK || ModeFromDB("notify") != plansv1.QuotaMode_QUOTA_MODE_NOTIFY || WindowFromDB("month") != plansv1.QuotaWindow_QUOTA_WINDOW_MONTH {
		t.Fatal("stored names do not map back")
	}
}

func TestValidatePlanVersionQuotaRefusals(t *testing.T) {
	scales := map[string]int{"api_calls": 0, "gb_hours": 3}
	cases := map[string]*plansv1.PublishPlanVersionRequest{
		// Soft bills what is over the limit, so the meter needs a price.
		"entitlements[0].mode":                 {BaseFeeMinor: 100, Entitlements: []*plansv1.Entitlement{{MeterCode: "gb_hours", Limit: "1", Mode: plansv1.QuotaMode_QUOTA_MODE_SOFT}}},
		"entitlements[0].window":               {BaseFeeMinor: 100, Entitlements: []*plansv1.Entitlement{{MeterCode: "gb_hours", Limit: "1", Window: plansv1.QuotaWindow(42)}}},
		"entitlements[0].alert_thresholds_pct": {BaseFeeMinor: 100, Entitlements: []*plansv1.Entitlement{{MeterCode: "gb_hours", Limit: "1", AlertThresholdsPct: []int32{80, 50}}}},
		"prices[0].included":                   {Prices: []*plansv1.Price{func() *plansv1.Price { p := perUnit("gb_hours", "1"); p.Included = "0.0001"; return p }()}},
	}
	for want, req := range cases {
		_, err := ValidatePlanVersion(req, scales)
		if got := fields(t, err); !contains(got, want) {
			t.Errorf("want violation %s, got %v", want, got)
		}
	}
}

func TestValidateMeterCategoryAndUnit(t *testing.T) {
	req := &plansv1.CreateMeterRequest{Code: "tokens", Aggregation: plansv1.Aggregation_AGGREGATION_SUM, ValueProperty: "n", UnitLabel: " Jeton (giriş) ", Category: " Yapay zekâ "}
	in, err := ValidateMeter(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if in.Category != "Yapay zekâ" || in.UnitLabel != "Jeton (giriş)" {
		t.Fatalf("meter input = %+v", in)
	}
	for _, bad := range []*plansv1.CreateMeterRequest{
		{Code: "tokens", Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: "calls", Category: strings.Repeat("x", 65)},
		{Code: "tokens", Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: "calls", Category: "a\nb"},
	} {
		if _, err := ValidateMeter(bad, -1); !contains(fields(t, err), "category") {
			t.Errorf("category %q accepted", bad.GetCategory())
		}
	}
}
