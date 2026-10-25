package domain

import (
	"strings"
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func TestEInvoiceOnAcceptsOnlyTheThreeSettings(t *testing.T) {
	for _, ok := range []string{"off", "finalized", "paid"} {
		if err := ValidateEInvoiceOn(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "always", "Paid"} {
		if errkey.Of(ValidateEInvoiceOn(bad)) != "einvoice.setting.invalid" {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestTaxIDsAreLettersAndDigits(t *testing.T) {
	for _, ok := range []string{"", "1234567890", "12345678901", "DE123456789"} {
		if err := ValidateTaxID(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"123", "12 345 6789", "1234-5678", strings.Repeat("1", 21)} {
		if errkey.Of(ValidateTaxID(bad)) != "plans.customer.tax_invalid" {
			t.Errorf("%q was accepted", bad)
		}
	}
	if err := ValidateCustomerTax(&plansv1.SetCustomerTaxRequest{TaxId: "1234567890", TaxOffice: "Kadikoy\nx"}); err == nil {
		t.Error("a multi-line tax office was accepted")
	}
	if err := ValidateLegal(&plansv1.LegalInfo{LegalName: "Acme A.S.", TaxId: "1234567890"}); err != nil {
		t.Error(err)
	}
	if err := ValidateLegal(&plansv1.LegalInfo{MersisNo: strings.Repeat("x", 201)}); err == nil {
		t.Error("a long MERSIS number was accepted")
	}
}
