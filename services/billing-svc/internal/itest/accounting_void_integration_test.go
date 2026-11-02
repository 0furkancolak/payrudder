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
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_invoice_id, currency, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
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
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider)
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

// A void gives back the tax of the invoice, as a credit note gives back the tax of what it credits: 120 paid with 20 tax and then voided and refunded is Tax 0 and Net 0, not
// Tax 20 and Net -20. The tax a void returns is all the invoice's tax its credit notes did not, split between the card part and the hand part by their shares.
func testAccountingGivesBackTheTaxOfAVoid(t *testing.T) {
	infra(t)
	svc := accounting.New(shared.app, shared.system)
	tid := uuid.New()
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: tid})
	paid, voided := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	summary := func() *billingv1.AccountingCurrency {
		t.Helper()
		r, err := svc.GetAccountingSummary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: "2026-10-01", ToDate: "2026-10-31"})
		if err != nil || len(r.GetCurrencies()) != 1 {
			t.Fatalf("summary: %v, %v", r, err)
		}
		return r.GetCurrencies()[0]
	}

	// 100 + 20 tax by card, voided and refunded.
	seedPaidInvoice(t, tid, "EUR", 120, 20, paid, voided)
	if c := summary(); c.GetGrossIncomeMinor() != 120 || c.GetTaxMinor() != 0 || c.GetRefundsMinor() != 120 || c.GetNetIncomeMinor() != 0 {
		t.Fatalf("a card invoice voided: gross %d tax %d refunds %d net %d; want 120, 0, 120, 0", c.GetGrossIncomeMinor(), c.GetTaxMinor(), c.GetRefundsMinor(), c.GetNetIncomeMinor())
	}

	// 1000 + 200 tax, 400 of it received by hand and 800 by card: the void refunds 800 and makes 400 a credit balance; the 200 of tax is split 133 and 67 and adds up.
	mixed := seedPaidInvoice(t, tid, "EUR", 1200, 200, paid, voided)
	seedVoidCredit(t, tid, mixed, "EUR", 400, voided)
	// 500 + 100 tax, a credit note of 300 that gave back 50 of the tax, then voided: the void refunds the other 300 and the other 50.
	noted := seedPaidInvoice(t, tid, "EUR", 600, 100, paid, voided)
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO credit_notes (tenant_id, id, customer_id, invoice_id, number, currency, total_minor, tax_minor, created_at)
		VALUES ($1,$2,$3,$4,$5,'EUR',300,50,$6)`, tid, uuid.New(), uuid.New(), noted, "CN-"+uuid.NewString()[:8], voided.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Paid in full by hand (400 + 80 tax) and voided: no provider refund, and the balance gives back all the tax.
	hand := seedPaidInvoice(t, tid, "EUR", 480, 80, paid, time.Time{})
	seedVoidCredit(t, tid, hand, "EUR", 480, voided)

	c := summary()
	if c.GetGrossIncomeMinor() != 120+1200+600+480 || c.GetRefundsMinor() != 120+1200+600+480 {
		t.Fatalf("gross %d refunds %d; want %d for both: everything paid was given back", c.GetGrossIncomeMinor(), c.GetRefundsMinor(), 120+1200+600+480)
	}
	if c.GetTaxMinor() != 0 || c.GetNetIncomeMinor() != 0 {
		t.Fatalf("tax %d net %d after every invoice was given back; want 0 and 0", c.GetTaxMinor(), c.GetNetIncomeMinor())
	}
}
