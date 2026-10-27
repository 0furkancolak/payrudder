//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Two tenants may both have an invoice "INV-2026-000001", and the bank-transfer reference is the invoice number: the same (provider, kind, ref, status) in
// two tenants' ledgers is two facts, not one. A tenant's facts must not swallow another tenant's, nor count as its outcomes.
func TestLedgerFactsAreUniquePerTenant(t *testing.T) {
	pg := testkit.StartPostgres(t)
	ctx := context.Background()
	ledger := store.Ledger{DB: openDB(t, pg).DB}

	a, b := uuid.New(), uuid.New()
	entry := func(tenant uuid.UUID, status string) store.LedgerEntry {
		return store.LedgerEntry{TenantID: tenant, InvoiceID: uuid.New(), Provider: "banktransfer", Environment: "live", Kind: "charge", Status: status,
			AmountMinor: 1000, Currency: "EUR", ProviderRef: "INV-2026-000001", Source: "command"}
	}
	for _, tenant := range []uuid.UUID{a, b} {
		fresh, err := ledger.Append(ctx, entry(tenant, "pending"))
		if err != nil || !fresh {
			t.Fatalf("tenant %s: pending row written = %v, %v; want a new row", tenant, fresh, err)
		}
	}
	// The same fact again, within one tenant, is still one row.
	if fresh, err := ledger.Append(ctx, entry(a, "pending")); err != nil || fresh {
		t.Fatalf("a repeated fact of one tenant: fresh = %v, %v; want false", fresh, err)
	}
	// Tenant A's outcome does not make tenant B's reference look contradictory.
	if _, err := ledger.Append(ctx, entry(a, "succeeded")); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, entry(b, "declined")); err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []uuid.UUID{a, b} {
		if n, err := ledger.Outcomes(ctx, tenant, "banktransfer", "charge", "INV-2026-000001"); err != nil || n != 1 {
			t.Fatalf("tenant %s: outcomes = %d, %v; want 1", tenant, n, err)
		}
	}
}
