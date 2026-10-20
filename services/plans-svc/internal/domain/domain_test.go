package domain

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

// fields returns the violated field names of an InvalidArgument error.
func fields(t *testing.T, err error) []string {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
	var out []string
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			for _, fv := range br.GetFieldViolations() {
				out = append(out, fv.GetField())
			}
		}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestValidateCreateAccountRequiresExplicitChoices(t *testing.T) {
	_, err := ValidateCreateAccount(&plansv1.CreateAccountRequest{Name: "Acme"})
	got := fields(t, err)
	for _, f := range []string{"currency", "timezone", "grace_period"} {
		if !contains(got, f) {
			t.Errorf("missing violation for %s: %v", f, got)
		}
	}

	s, err := ValidateCreateAccount(&plansv1.CreateAccountRequest{
		Name: "Acme", Currency: "TRY", Timezone: "Europe/Istanbul", GracePeriod: durationpb.New(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Tier != TierFree || s.Grace != 24*time.Hour {
		t.Fatalf("settings = %+v", s)
	}

	_, err = ValidateCreateAccount(&plansv1.CreateAccountRequest{
		Name: "Acme", Currency: "XYZ", Timezone: "Mars/Olympus", GracePeriod: durationpb.New(8 * 24 * time.Hour),
	})
	if got := fields(t, err); len(got) != 3 {
		t.Fatalf("violations = %v", got)
	}
}

func TestPaymentMethodByEnvironment(t *testing.T) {
	if err := ValidatePaymentMethod("sandbox", "pm_sandbox_declined"); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePaymentMethod("sandbox", "tok_real_visa"); err == nil {
		t.Fatal("sandbox accepted a non-scenario token")
	}
	if err := ValidatePaymentMethod("live", "pm_sandbox_success"); err == nil {
		t.Fatal("live accepted a scenario token")
	}
	if err := ValidatePaymentMethod("live", "tok_123"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMeter(t *testing.T) {
	ok := &plansv1.CreateMeterRequest{Code: "gb_hours", Aggregation: plansv1.Aggregation_AGGREGATION_SUM,
		ValueProperty: "gb_h", QuantityScale: 3, UnitLabel: "GB-h",
		Filter: map[string]*plansv1.FilterValues{"region": {Values: []string{"eu", "us", "eu"}}}}
	in, err := ValidateMeter(ok, -1)
	if err != nil {
		t.Fatal(err)
	}
	if string(in.Filter) != `{"region":["eu","us"]}` || *in.ValueProperty != "gb_h" {
		t.Fatalf("meter input = %+v filter=%s", in, in.Filter)
	}
	if _, err := ValidateMeter(ok, 2); !contains(fields(t, err), "quantity_scale") {
		t.Fatal("scale change across versions accepted")
	}
	count := &plansv1.CreateMeterRequest{Code: "api_calls", Aggregation: plansv1.Aggregation_AGGREGATION_COUNT,
		ValueProperty: "x", UnitLabel: "calls"}
	if !contains(fields(t, func() error { _, e := ValidateMeter(count, -1); return e }()), "value_property") {
		t.Fatal("COUNT with value_property accepted")
	}
	bad := &plansv1.CreateMeterRequest{Code: "Bad-Code", QuantityScale: 7}
	got := fields(t, func() error { _, e := ValidateMeter(bad, -1); return e }())
	for _, f := range []string{"code", "aggregation", "quantity_scale", "unit_label"} {
		if !contains(got, f) {
			t.Errorf("missing violation for %s: %v", f, got)
		}
	}
}

func tiered(tiers ...*plansv1.PriceTier) *plansv1.Price {
	return &plansv1.Price{MeterCode: "gb_hours", Model: &plansv1.Price_Tiered{Tiered: &plansv1.TieredGraduatedPrice{Tiers: tiers}}}
}

func TestValidatePlanVersionTiers(t *testing.T) {
	scales := map[string]int{"gb_hours": 3, "api_calls": 0}
	req := &plansv1.PublishPlanVersionRequest{
		BaseFeeMinor: 9900,
		Prices: []*plansv1.Price{
			tiered(&plansv1.PriceTier{UpTo: "10.5", UnitPrice: "0"}, &plansv1.PriceTier{UnitPrice: "12.5", FlatFeeMinor: 100}),
			{MeterCode: "api_calls", Model: &plansv1.Price_Package{Package: &plansv1.PackagePrice{PackageSize: "1000", PackagePriceMinor: 500}}},
		},
		Entitlements: []*plansv1.Entitlement{{MeterCode: "gb_hours", Limit: "100.25"}},
	}
	in, err := ValidatePlanVersion(req, scales)
	if err != nil {
		t.Fatal(err)
	}
	if in.Entitlements[0].LimitUnits != 100250 || len(in.Entitlements[0].Thresholds) != 2 {
		t.Fatalf("entitlement = %+v", in.Entitlements[0])
	}
	if !strings.Contains(string(in.Prices[0].Config), `"up_to_units":10500`) {
		t.Fatalf("tier config = %s", in.Prices[0].Config)
	}
	back, err := PriceFromStored("gb_hours", in.Prices[0].Model, in.Prices[0].Config, 3)
	if err != nil || back.GetTiered().GetTiers()[0].GetUpTo() != "10.500" {
		t.Fatalf("round trip = %v, %v", back, err)
	}

	cases := map[string]*plansv1.Price{
		"prices[0].tiered.tiers[1].up_to":      tiered(&plansv1.PriceTier{UpTo: "10", UnitPrice: "1"}, &plansv1.PriceTier{UpTo: "5", UnitPrice: "1"}, &plansv1.PriceTier{UnitPrice: "1"}),
		"prices[0].tiered.tiers[0].up_to":      tiered(&plansv1.PriceTier{UnitPrice: "1"}, &plansv1.PriceTier{UnitPrice: "1"}),
		"prices[0].tiered.tiers[1].unit_price": tiered(&plansv1.PriceTier{UpTo: "1", UnitPrice: "1"}, &plansv1.PriceTier{UnitPrice: "-1"}),
	}
	for want, price := range cases {
		_, err := ValidatePlanVersion(&plansv1.PublishPlanVersionRequest{Prices: []*plansv1.Price{price}}, scales)
		if got := fields(t, err); !contains(got, want) {
			t.Errorf("want violation %s, got %v", want, got)
		}
	}

	_, err = ValidatePlanVersion(&plansv1.PublishPlanVersionRequest{
		Prices:       []*plansv1.Price{{MeterCode: "nope", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "1"}}}},
		Entitlements: []*plansv1.Entitlement{{MeterCode: "gb_hours", Limit: "1.2345"}},
	}, scales)
	got := fields(t, err)
	if !contains(got, "prices[0].meter_code") || !contains(got, "entitlements[0].limit") {
		t.Fatalf("violations = %v", got)
	}
}

func TestValidateAddress(t *testing.T) {
	ok := &plansv1.Address{Line1: "Atatürk Cd. 1", City: "İstanbul", PostalCode: "34000", Country: "TR"}
	if err := ValidateAddress("address", ok); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAddress("address", nil); err != nil {
		t.Fatalf("an absent address is fine: %v", err)
	}
	bad := &plansv1.Address{Line1: strings.Repeat("x", 121), Line2: "two\nlines", PostalCode: strings.Repeat("9", 21), Country: "turkey"}
	got := fields(t, ValidateAddress("address", bad))
	for _, f := range []string{"address.line1", "address.line2", "address.postal_code", "address.country"} {
		if !contains(got, f) {
			t.Errorf("missing violation for %s: %v", f, got)
		}
	}
}
