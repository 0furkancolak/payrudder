package domain

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func TestNormalizeBillingProfile(t *testing.T) {
	a, l := NormalizeBillingProfile(
		&plansv1.Address{Line1: " Hauptstr. 1 ", City: " Berlin ", Country: " de "},
		&plansv1.LegalInfo{LegalName: " Shop GmbH ", TaxId: "de 123-456.789", TaxOffice: " FA "})
	if a.GetLine1() != "Hauptstr. 1" || a.GetCity() != "Berlin" || a.GetCountry() != "DE" {
		t.Errorf("address = %v", a)
	}
	if l.GetLegalName() != "Shop GmbH" || l.GetTaxId() != "DE123456789" || l.GetTaxOffice() != "FA" {
		t.Errorf("legal = %v", l)
	}
	if a, l := NormalizeBillingProfile(nil, nil); a.GetCountry() != "" || l.GetTaxId() != "" {
		t.Errorf("nil profile = %v %v", a, l)
	}
}

func TestValidateBillingProfile(t *testing.T) {
	de := &plansv1.Address{Line1: "Hauptstr. 1", City: "Berlin", Country: "DE"}
	for name, tc := range map[string]struct {
		address *plansv1.Address
		legal   *plansv1.LegalInfo
		key     string
	}{
		"complete":        {de, &plansv1.LegalInfo{LegalName: "Shop", TaxId: "DE123456789"}, ""},
		"outside the EU":  {&plansv1.Address{Line1: "1 Main St", City: "Austin", Country: "US"}, &plansv1.LegalInfo{LegalName: "Shop"}, ""},
		"no name":         {de, &plansv1.LegalInfo{TaxId: "DE123456789"}, "identity.billing_profile.legal_name_required"},
		"no country":      {&plansv1.Address{Line1: "x", City: "y"}, &plansv1.LegalInfo{LegalName: "Shop"}, "identity.billing_profile.country_required"},
		"no street":       {&plansv1.Address{City: "Berlin", Country: "DE"}, &plansv1.LegalInfo{LegalName: "Shop", TaxId: "DE123456789"}, "identity.billing_profile.address_required"},
		"no city":         {&plansv1.Address{Line1: "x", Country: "DE"}, &plansv1.LegalInfo{LegalName: "Shop", TaxId: "DE123456789"}, "identity.billing_profile.address_required"},
		"no tax id in TR": {&plansv1.Address{Line1: "x", City: "y", Country: "TR"}, &plansv1.LegalInfo{LegalName: "Shop"}, "identity.billing_profile.tax_id_required"},
		"a bad tax id":    {de, &plansv1.LegalInfo{LegalName: "Shop", TaxId: "DE-1!"}, "plans.customer.tax_invalid"},
		"a bad country":   {&plansv1.Address{Line1: "x", City: "y", Country: "Germany"}, &plansv1.LegalInfo{LegalName: "Shop", TaxId: "DE123456789"}, "gateway.request.invalid"},
	} {
		err := ValidateBillingProfile(tc.address, tc.legal)
		switch {
		case tc.key == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.key != "" && status.Code(err) != codes.InvalidArgument:
			t.Errorf("%s: code %v, want InvalidArgument (%v)", name, status.Code(err), err)
		case tc.key != "" && tc.key != "gateway.request.invalid" && errkey.Of(err) != tc.key:
			t.Errorf("%s: key %q, want %q", name, errkey.Of(err), tc.key)
		}
	}
}

// A profile that ValidateBillingProfile accepts is one the checklist counts, and the other way round for the parts the checklist reads.
func TestBillingProfileMatchesTheChecklist(t *testing.T) {
	a, l := NormalizeBillingProfile(&plansv1.Address{Line1: "x", City: "y", Country: "fr"}, &plansv1.LegalInfo{LegalName: "Shop", TaxId: "FR 12 345 678 901"})
	if err := ValidateBillingProfile(a, l); err != nil || !LegalProfileComplete(a, l) {
		t.Errorf("a valid profile: %v, complete %v", err, LegalProfileComplete(a, l))
	}
	if TaxIDRequired("us") || !TaxIDRequired(" tr ") {
		t.Error("TaxIDRequired")
	}
}
