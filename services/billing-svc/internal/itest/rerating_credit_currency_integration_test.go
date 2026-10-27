//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// A credit from re-rating has the currency of the invoices it corrects, so it is not taken off an invoice in another currency.
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
}
