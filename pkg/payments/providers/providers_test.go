package providers_test

import (
	"slices"
	"testing"

	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/pkg/payments/providers"
)

func TestCatalogue(t *testing.T) {
	if got := providers.Names(); !slices.Equal(got, []string{"iyzico", "paddle"}) {
		t.Fatalf("Names = %v", got)
	}
	iy, ok := providers.Lookup("iyzico")
	if !ok || iy.Owner() != payments.OwnerQuotify || !iy.CanCharge("TRY") || iy.CanCharge("JPY") {
		t.Errorf("iyzico = %+v", iy)
	}
	pd, ok := providers.Lookup("paddle")
	if !ok || pd.Owner() != payments.OwnerProvider || !pd.CanCharge("USD") || pd.CanCharge("TRY") {
		t.Errorf("paddle = %+v", pd)
	}
	if _, ok := providers.Lookup("nope"); ok {
		t.Error("an unknown provider is not in the catalogue")
	}
}

func TestWhoMailsReceipts(t *testing.T) {
	iy, _ := providers.Lookup("iyzico")
	pd, _ := providers.Lookup("paddle")
	if iy.SendsOwnReceipts || !pd.SendsOwnReceipts {
		t.Fatalf("iyzico %v, paddle %v: Paddle is the merchant of record and mails its own receipts, iyzico does not", iy.SendsOwnReceipts, pd.SendsOwnReceipts)
	}
}
