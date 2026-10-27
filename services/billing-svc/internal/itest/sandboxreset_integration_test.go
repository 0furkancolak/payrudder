//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	billingreset "github.com/0furkancolak/payrudder/services/billing-svc/internal/sandboxreset"
)

// testSandboxResetKeepsConfigurationAndOtherTenants: a reset takes a sandbox tenant's invoices and everything on them (sagas, credit notes, credits, numbering) and
// nothing else: its credit packs and dunning settings stay, another tenant's data is untouched, and a second reset finds nothing. Every table that holds tenant rows
// is either deleted from or kept on purpose.
func testSandboxResetKeepsConfigurationAndOtherTenants(t *testing.T) {
	infra(t)
	ctx := context.Background()
	sbx, other := uuid.New(), uuid.New()
	when := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

	// Two tenants with the same kinds of data.
	for _, tid := range []uuid.UUID{sbx, other} {
		paid := seedPaidInvoice(t, tid, "USD", 1000, 0, when, time.Time{})
		seedPaidInvoice(t, tid, "USD", 700, 0, when, time.Time{})
		seedCreditNote(t, tid, paid, "USD", 100, when)
		if _, err := shared.sql.Exec(ctx, `INSERT INTO invoice_number_seq (tenant_id, year, next_value) VALUES ($1, 2026, 3)`, tid); err != nil {
			t.Fatal(err)
		}
		// Configuration is written the way the product writes it: as the tenant, on the app pool.
		if err := pgxx.InTenantTx(ctx, shared.app, tid, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO credit_packs (tenant_id, id, name, credits, price_minor, currency) VALUES (app_tenant_id(), gen_random_uuid(), 'Starter', 100, 500, 'USD')`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO dunning_settings (tenant_id, retry_hours) VALUES (app_tenant_id(), '{24,72}')`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(table string, tid uuid.UUID) int {
		t.Helper()
		var n int
		// The table name is one of the constants below, never input.
		if err := shared.sql.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	counts := func(tid uuid.UUID) map[string]int {
		out := map[string]int{}
		for _, tb := range []string{"invoices", "invoice_sagas", "saga_events", "credit_notes", "invoice_number_seq", "credit_packs", "dunning_settings"} {
			out[tb] = count(tb, tid)
		}
		return out
	}
	otherBefore := counts(other)
	if c := counts(sbx); c["invoices"] != 2 || c["invoice_sagas"] != 2 || c["credit_notes"] != 1 || c["credit_packs"] != 1 {
		t.Fatalf("the fixture is not what the test expects: %v", c)
	}

	r := billingreset.New(shared.system)
	deleted, err := r.Reset(ctx, sbx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted["invoices"] != 2 || deleted["credit_notes"] != 1 || deleted["invoice_number_seq"] != 1 {
		t.Errorf("deleted = %v", deleted)
	}
	after := counts(sbx)
	for _, tb := range []string{"invoices", "invoice_sagas", "saga_events", "credit_notes", "invoice_number_seq"} {
		if after[tb] != 0 {
			t.Errorf("%s still has %d rows of the reset tenant", tb, after[tb])
		}
	}
	if after["credit_packs"] != 1 || after["dunning_settings"] != 1 {
		t.Errorf("configuration was deleted: %v", after)
	}
	for tb, n := range otherBefore {
		if got := counts(other)[tb]; got != n {
			t.Errorf("another tenant's %s went from %d to %d", tb, n, got)
		}
	}
	if again, err := r.Reset(ctx, sbx); err != nil || len(again) != 0 {
		t.Errorf("a second reset = %v, %v; want nothing to delete", again, err)
	}

	missing, err := sandboxreset.MissingTables(ctx, shared.systemPool, billingreset.Tables(), billingreset.Kept)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Errorf("tables with tenant rows that a reset neither deletes from nor keeps on purpose: %v (add them to queries/reset.sql, or to Kept)", missing)
	}
}
