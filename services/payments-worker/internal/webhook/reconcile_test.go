package webhook

import (
	"testing"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
)

// Several transfers of one invoice are one charge of the sum, not several charges; other providers' charges are left as they are.
func TestMergeTransfers(t *testing.T) {
	in := []store.Charge{
		{InvoiceID: "a", Provider: "banktransfer", ProviderRef: "a:T1", Currency: "TRY", AmountMinor: 400},
		{InvoiceID: "b", Provider: "iyzico", ProviderRef: "P1", Currency: "TRY", AmountMinor: 700},
		{InvoiceID: "a", Provider: "banktransfer", ProviderRef: "a:T2", Currency: "TRY", AmountMinor: 600},
		{InvoiceID: "c", Provider: "banktransfer", ProviderRef: "c:T1", Currency: "TRY", AmountMinor: 50},
	}
	got := mergeTransfers(in)
	if len(got) != 3 {
		t.Fatalf("merged = %+v, want 3 charges (a's two transfers are one)", got)
	}
	if got[0].InvoiceID != "a" || got[0].AmountMinor != 1000 || got[1].Provider != "iyzico" || got[1].AmountMinor != 700 || got[2].InvoiceID != "c" || got[2].AmountMinor != 50 {
		t.Errorf("merged = %+v", got)
	}
}

// A paid invoice with no charge in the ledger is a discrepancy, unless billing-svc recorded a payment by hand for it.
func TestPaidByHandIsNotPaidWithoutCharge(t *testing.T) {
	paid := map[string]*billingv1.Invoice{
		"hand":  {Id: "hand", Number: "INV-1", TotalMinor: 500, Currency: "TRY"},
		"ghost": {Id: "ghost", Number: "INV-2", TotalMinor: 500, Currency: "TRY"},
	}
	rec := &Reconciliation{Counts: map[string]int{}}
	rec.compare(paid, map[string][]ledgerCharge{}, func(id string) bool { return id == "hand" })
	if rec.Counts[PaidWithoutCharge] != 1 || len(rec.Discrepancies) != 1 || rec.Discrepancies[0].InvoiceID != "ghost" {
		t.Fatalf("discrepancies = %+v, want only the invoice nobody recorded a payment for", rec.Discrepancies)
	}
}

// The transfers of one invoice add up to its total: no amount mismatch.
func TestSummedTransfersMatchTheInvoice(t *testing.T) {
	charges := mergeTransfers([]store.Charge{
		{InvoiceID: "a", Provider: "banktransfer", ProviderRef: "a:T1", Currency: "TRY", AmountMinor: 400},
		{InvoiceID: "a", Provider: "banktransfer", ProviderRef: "a:T2", Currency: "TRY", AmountMinor: 600},
	})
	by := map[string][]ledgerCharge{}
	for _, c := range charges {
		by[c.InvoiceID] = append(by[c.InvoiceID], ledgerCharge(c))
	}
	rec := &Reconciliation{Counts: map[string]int{}}
	rec.compare(map[string]*billingv1.Invoice{"a": {Id: "a", TotalMinor: 1000, Currency: "TRY"}}, by, func(string) bool { return false })
	if len(rec.Discrepancies) != 0 {
		t.Fatalf("discrepancies = %+v, want none", rec.Discrepancies)
	}
}
