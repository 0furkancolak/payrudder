package query

import (
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

const (
	mkc = plansv1.PromoteChange_ACTION_CREATE
	sme = plansv1.PromoteChange_ACTION_UNCHANGED
	cfl = plansv1.PromoteChange_ACTION_CONFLICT
	skp = plansv1.PromoteChange_ACTION_SKIPPED
)

func actions(ds []Decision) map[string]plansv1.PromoteChange_Action {
	out := map[string]plansv1.PromoteChange_Action{}
	for _, d := range ds {
		out[d.Key] = d.Action
	}
	return out
}

func TestMetersAreMatchedByCodeAndOnlyAdded(t *testing.T) {
	sb := []*plansv1.Meter{
		{Code: "a", Version: 3, Aggregation: plansv1.Aggregation_AGGREGATION_SUM, ValueProperty: "v", QuantityScale: 2, UnitLabel: "GB"},
		{Code: "b", Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: "calls"},
		{Code: "c", Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: "new"},
	}
	live := []*plansv1.Meter{
		{Code: "a", Version: 1, Aggregation: plansv1.Aggregation_AGGREGATION_SUM, ValueProperty: "v", QuantityScale: 2, UnitLabel: "GB"}, // same terms, another version
		{Code: "b", Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: "requests"},                                           // differs
	}
	got := actions(Meters(sb, live))
	if got["a"] != sme || got["b"] != cfl || got["c"] != mkc || len(got) != 3 {
		t.Errorf("meters = %v", got)
	}
}

func TestPlansIgnoreIdsVersionsAndOrder(t *testing.T) {
	price := func(code, unit string) *plansv1.Price {
		return &plansv1.Price{MeterCode: code, Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: unit}}}
	}
	v1 := &plansv1.PlanVersion{Id: "x", PlanId: "p", Version: 4, BaseFeeMinor: 100, Prices: []*plansv1.Price{price("a", "1"), price("b", "2")},
		Fees: []*plansv1.CurrencyFee{{Currency: "USD", BaseFeeMinor: 5}, {Currency: "EUR", BaseFeeMinor: 6}}}
	v2 := &plansv1.PlanVersion{Id: "y", PlanId: "q", Version: 1, BaseFeeMinor: 100, Prices: []*plansv1.Price{price("b", "2"), price("a", "1")},
		Fees: []*plansv1.CurrencyFee{{Currency: "EUR", BaseFeeMinor: 6}, {Currency: "USD", BaseFeeMinor: 5}}}
	other := &plansv1.PlanVersion{Version: 1, BaseFeeMinor: 999}
	p := func(code, name string, v *plansv1.PlanVersion) PlanSet {
		return PlanSet{Plan: &plansv1.Plan{Code: code, Name: name}, Version: v}
	}
	got := actions(Plans(
		[]PlanSet{p("same", "S", v1), p("moved", "M", v1), p("renamed", "R", v1), p("new", "N", v1), p("draft", "D", nil), p("emptylive", "E", v1)},
		[]PlanSet{p("same", "S", v2), p("moved", "M", other), p("renamed", "Other", v2), p("emptylive", "E", nil)}))
	want := map[string]plansv1.PromoteChange_Action{"same": sme, "moved": cfl, "renamed": cfl, "new": mkc, "draft": skp, "emptylive": mkc}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("plan %s = %v, want %v", k, got[k], w)
		}
	}
}

func TestTaxesCouponsMailAndWebhooks(t *testing.T) {
	tax := actions(Taxes(
		[]*plansv1.TaxRate{{Country: "TR", Name: "KDV", RateBps: 2000}, {Country: "DE", Name: "VAT", RateBps: 1900}, {Country: "", Name: "Other", RateBps: 0}},
		[]*plansv1.TaxRate{{Country: "TR", Name: "KDV", RateBps: 1800}, {Country: "DE", Name: "VAT", RateBps: 1900}}))
	if tax["TR"] != cfl || tax["DE"] != sme || tax["*"] != mkc {
		t.Errorf("taxes = %v", tax)
	}

	cp := actions(Coupons(
		[]*plansv1.Coupon{{Code: "A", PercentBps: 1000, Active: true, Redeemed: 7}, {Code: "B", PercentBps: 500, Active: true}, {Code: "C", Active: false}, {Code: "D", PercentBps: 10, Active: true}},
		[]*plansv1.Coupon{{Code: "A", PercentBps: 1000, Active: true, Redeemed: 0}, {Code: "B", PercentBps: 600, Active: true}}))
	if cp["A"] != sme || cp["B"] != cfl || cp["C"] != skp || cp["D"] != mkc {
		t.Errorf("coupons = %v", cp)
	}

	off := func(cats ...string) []*plansv1.MailCategorySetting {
		out := []*plansv1.MailCategorySetting{{Category: "invoice_issued", Enabled: true}, {Category: "usage_alerts", Enabled: true}}
		for _, c := range cats {
			for _, s := range out {
				if s.Category == c {
					s.Enabled = false
				}
			}
		}
		return out
	}
	for name, tc := range map[string]struct {
		sandbox, live []*plansv1.MailCategorySetting
		want          plansv1.PromoteChange_Action
	}{
		"both default":                    {off(), off(), sme},
		"same choice":                     {off("usage_alerts"), off("usage_alerts"), sme},
		"live untouched takes the choice": {off("usage_alerts"), off(), mkc},
		"live chose otherwise":            {off("usage_alerts"), off("invoice_issued"), cfl},
	} {
		if got := Mail(tc.sandbox, tc.live)[0].Action; got != tc.want {
			t.Errorf("mail, %s: %v, want %v", name, got, tc.want)
		}
	}

	hooks := func(active bool) map[string]plansv1.PromoteChange_Action {
		return actions(Webhooks(
			[]*plansv1.WebhookEndpoint{{Url: "https://a", EventTypes: []string{"x", "y"}}, {Url: "https://b"}, {Url: "https://c", EventTypes: []string{"x"}}},
			[]*plansv1.WebhookEndpoint{{Url: "https://a", EventTypes: []string{"y", "x"}}, {Url: "https://c", EventTypes: []string{"z"}}}, active))
	}
	if h := hooks(true); h["https://a"] != sme || h["https://b"] != mkc || h["https://c"] != cfl {
		t.Errorf("webhooks (active) = %v", h)
	}
	if h := hooks(false); h["https://b"] != skp {
		t.Errorf("webhooks (inactive) = %v: nothing is added to an inactive environment", h)
	}
}
