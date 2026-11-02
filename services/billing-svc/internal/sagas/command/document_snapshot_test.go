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
