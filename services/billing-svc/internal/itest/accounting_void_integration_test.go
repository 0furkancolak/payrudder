//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/accounting"
)

// seedVoidCredit is the credit balance a void makes of the money received by hand (source_invoice_id), made at at.
func seedVoidCredit(t *testing.T, tid, invoice uuid.UUID, currency string, amount int64, at time.Time) {
	t.Helper()
	if _, err := shared.system.Exec(context.Background(), `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_invoice_id, currency, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		tid, uuid.New(), uuid.New(), amount, invoice, currency, at); err != nil {
		t.Fatal(err)
	}
}

// What a void gave back is counted once in the refunds of the accounting summary: the card part on the day of the refund (less the credit notes, which are counted on
// their own day), the part received by hand on the day it became a credit balance. An invoice that was never paid in full is not income, so its balance is no refund.
func testAccountingCountsWhatAVoidGaveBackOnce(t *testing.T) {
	infra(t)
	svc := accounting.New(shared.app, shared.system)
	tid := uuid.New()
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: tid})
	paid, voided := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

	// Card, 400 credited by a note, then voided: the note is 400 and the refund of the rest 600, not 1000 again.
	card := seedPaidInvoice(t, tid, "EUR", 1000, 0, paid, voided)
	seedCreditNote(t, tid, card, "EUR", 400, voided.Add(-24*time.Hour))
	// Paid in full by hand, then voided: no provider refund, a credit balance of 1000.
	hand := seedPaidInvoice(t, tid, "EUR", 1000, 0, paid, time.Time{})
	seedVoidCredit(t, tid, hand, "EUR", 1000, voided)
	// Half by hand, half by card: 600 went back to the card, 400 became a balance.
	mixed := seedPaidInvoice(t, tid, "EUR", 1000, 0, paid, voided)
	seedVoidCredit(t, tid, mixed, "EUR", 400, voided)
	// Part paid by hand and voided before it was paid: never income, so the balance is no refund.
	part := uuid.New()
	if _, err := shared.system.Exec(context.Background(), `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider)
		VALUES ($1,$2,$3,'2026-09','REGULAR',$4,'EUR',1000,1000,'void','{"customer_external_id":"cus_part","environment":"live"}'::jsonb,'')`, tid, part, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	seedVoidCredit(t, tid, part, "EUR", 600, voided)

	r, err := svc.GetAccountingSummary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: "2026-10-01", ToDate: "2026-10-31"})
	if err != nil || len(r.GetCurrencies()) != 1 {
		t.Fatalf("summary: %v, %v", r, err)
	}
	eur := r.GetCurrencies()[0]
	// Paid: three invoices of 1000. Given back: 400 (note) + 600 (card refund of the first) + 1000 (hand) + 600 (card refund of the mixed) + 400 (its balance).
	if eur.GetGrossIncomeMinor() != 3000 || eur.GetRefundsMinor() != 3000 || eur.GetNetIncomeMinor() != 0 {
		t.Fatalf("EUR gross %d refunds %d net %d; want 3000, 3000, 0", eur.GetGrossIncomeMinor(), eur.GetRefundsMinor(), eur.GetNetIncomeMinor())
	}
}
