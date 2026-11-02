package store

import (
	"testing"

	"github.com/google/uuid"
)

// The primary account of an integrator keeps the form its settings were sealed in before an environment could hold several, so nothing stored earlier has to be
// re-sealed; a labelled account adds its key, so its settings do not open as another account's.
func TestEInvoiceSealingKeepsTheOldFormForThePrimaryAccount(t *testing.T) {
	tenant := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	if got, want := string(einvoiceAAD(tenant, "live", "nilvera")), "11111111-2222-3333-4444-555555555555|einvoice|live"; got != want {
		t.Errorf("primary = %q, want the form that was sealed before labels: %q", got, want)
	}
	if a, b := string(einvoiceAAD(tenant, "live", "nilvera~tr")), string(einvoiceAAD(tenant, "live", "nilvera~de")); a == b || a == string(einvoiceAAD(tenant, "live", "nilvera")) {
		t.Errorf("labelled accounts must not share a seal: %q %q", a, b)
	}
}
