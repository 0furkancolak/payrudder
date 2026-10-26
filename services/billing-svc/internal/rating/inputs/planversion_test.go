package inputs

import (
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
)

func TestConvertPlanVersionCarriesTheIncludedQuantity(t *testing.T) {
	pv, err := ConvertPlanVersion(&plansv1.PlanVersion{Id: "pv", Prices: []*plansv1.Price{
		{MeterCode: "calls", Included: "1000", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "2"}}},
		{MeterCode: "gb", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "1"}}},
	}}, "TRY", "TRY")
	if err != nil {
		t.Fatal(err)
	}
	if inc := pv.Prices["calls"].Included; inc == nil || inc.Text('f') != "1000" {
		t.Fatalf("included = %v", inc)
	}
	if pv.Prices["gb"].Included != nil {
		t.Fatalf("a price without an included quantity got %v", pv.Prices["gb"].Included)
	}
	amount, err := rating.Amount(pv.Prices["calls"], pv.Prices["calls"].Included)
	if err != nil || amount != 0 {
		t.Fatalf("the included quantity costs %d, %v", amount, err)
	}
	if _, err := ConvertPlanVersion(&plansv1.PlanVersion{Prices: []*plansv1.Price{{MeterCode: "x", Included: "lots", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "1"}}}}}, "", ""); err == nil {
		t.Fatal("an unreadable included quantity was accepted")
	}
}
