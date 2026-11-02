package command

import (
	"testing"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
)

// The facts frozen on the saga reach the command the invoice worker renders from, so the PDF is in the customer's language and carries both tax ids.
func TestInvoiceSnapshotCarriesTheDocumentFacts(t *testing.T) {
	s := &saga{Number: "INV-1", Currency: "TRY", Snap: model.Snapshot{
		CustomerLocale: "tr", DefaultLocale: "de", CustomerType: "business", SellerTaxID: "1234567890", SellerTaxOffice: "Kadikoy", SellerCountry: "TR",
		CustomerTaxID: "DE136695976", CustomerTaxOffice: "Berlin", CustomerCountry: "DE", TimeZone: "Europe/Istanbul", CreditSurplusMinor: 1800}}
	got := (&Engine{}).invoiceSnapshot(s, nil)
	if got.GetLocale() != "tr" || got.GetDefaultLocale() != "de" || got.GetCustomerType() != "business" || got.GetSellerTaxId() != "1234567890" ||
		got.GetSellerTaxOffice() != "Kadikoy" || got.GetSellerCountry() != "TR" || got.GetCustomerTaxId() != "DE136695976" || got.GetCustomerTaxOffice() != "Berlin" || got.GetCustomerCountry() != "DE" ||
		got.GetTimeZone() != "Europe/Istanbul" || got.GetCreditSurplusMinor() != 1800 {
		t.Fatalf("snapshot = %v", got)
	}
}

// The amount and the period of an invoice mail are written the way the customer's language writes them (pkg/localefmt, the same table as the invoice PDF and the hosted
// pages); a customer with no language of their own gets the tenant's default one, and with neither, English.
func TestMailDataIsWrittenInTheCustomersLanguage(t *testing.T) {
	for _, tc := range []struct {
		name, customer, tenant, amount, period, locale string
	}{
		{"english", "", "", "1,234.56", "October 2026", ""},
		{"turkish customer", "tr", "", "1.234,56", "Ekim 2026", "tr"},
		{"german customer, french tenant", "de", "fr", "1.234,56", "Oktober 2026", "de"},
		{"no language, the tenant's", "", "fr", "1\u202f234,56", "octobre 2026", "fr"}, // a narrow no-break space groups the thousands in French
	} {
		s := &saga{Number: "INV-1", Currency: "EUR", PeriodID: "2026-10", TotalMinor: 123456, Snap: model.Snapshot{CurrencyExponent: 2, CustomerLocale: tc.customer, DefaultLocale: tc.tenant}}
		d := mailData(s)
		if d["amount"] != tc.amount || d["period"] != tc.period || d["locale"] != tc.locale {
			t.Errorf("%s: amount %q period %q locale %q; want %q, %q, %q", tc.name, d["amount"], d["period"], d["locale"], tc.amount, tc.period, tc.locale)
		}
	}
}
