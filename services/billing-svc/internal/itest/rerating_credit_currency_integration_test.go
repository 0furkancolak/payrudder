//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/migrations"
)

// A credit from re-rating has the currency of the invoices it corrects, so it is not taken off an invoice in another currency; credits written before that (without a
// currency) get theirs from migration 00047.
func testRerateCreditsAreInTheCurrencyOfTheInvoices(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	completeOctober(e) // USD invoices
	job, _ := e.rerate("2026-10", e.newVersion(800, "10", "1.6"))
	if job.Credits != 2 {
		t.Fatalf("job = %+v, want 2 credits", job)
	}
	ctx := context.Background()
	count := func(where string) int {
		var n int
		if err := shared.sql.QueryRow(ctx, `SELECT count(*) FROM credit_balances WHERE tenant_id = $1 AND source_rerating_job_id = $2 AND `+where, e.tid, job.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("currency = 'USD'"); n != 2 {
		t.Fatalf("%d of the 2 credits are in USD", n)
	}
	// Only an invoice in the credit's currency can use it.
	q := sqlc.New(shared.sql)
	for currency, want := range map[string]int{"USD": 1, "EUR": 0, "TRY": 0} {
		got, err := q.ListUnappliedCredits(ctx, sqlc.ListUnappliedCreditsParams{TenantID: e.tid, CustomerID: uuid.MustParse(e.customer("cus_a").Id), Currency: currency})
		if err != nil || len(got) != want {
			t.Errorf("credits usable in %s = %d (%v), want %d", currency, len(got), err, want)
		}
	}

	if cellsMode() {
		return // the rest runs a one-off data migration of the public schema; a tenant schema is born with the fixed code
	}
	// Credits written before the fix have no currency; the migration gives them the one of the customer's invoice of the period.
	migrator, err := pgx.Connect(ctx, shared.db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close(ctx)
	if _, err := shared.sql.Exec(ctx, `UPDATE credit_balances SET currency = NULL WHERE tenant_id = $1 AND source_rerating_job_id = $2`, e.tid, job.ID); err != nil {
		t.Fatal(err)
	}
	if n := count("currency IS NULL"); n != 2 {
		t.Fatalf("%d credits without a currency, want 2", n)
	}
	raw, err := migrations.FS.ReadFile("00047_rerating_credit_currency.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	up = up[strings.Index(up, "-- +goose Up")+len("-- +goose Up") : strings.Index(up, "-- +goose Down")]
	if _, err := migrator.Exec(ctx, up); err != nil {
		t.Fatalf("migration: %v", err)
	}
	if n := count("currency = 'USD'"); n != 2 {
		t.Fatalf("after the migration %d of the 2 credits are in USD", n)
	}
}
