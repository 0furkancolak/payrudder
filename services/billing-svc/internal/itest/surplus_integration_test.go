//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
)

// A negative total is no longer lost (docs/code-quality/billing-svc-status.md, "a negative total clamped to zero"): the invoice charges nothing, and what the lines came to
// below zero, tax included (the tax of what is taken back comes back too), becomes a credit balance of its own source, one per invoice, that the customer's next invoices use.
func testANegativeTotalBecomesACreditBalance(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	e.customer("cus_b").Country = "DE"
	e.setTax(&plansv1.TaxRate{Country: "DE", Name: "VAT", RateBps: 2000})
	e.addUsage("2026-10", "cus_b", "gb_hours", "200.000", 3) // 2500
	completeOctober(e)                                       // cus_b: 1000 + 2500 = 3500 and 700 tax
	if got := e.invoiceTotal("2026-10", "cus_b"); got != 4200 {
		t.Fatalf("october cus_b total = %d, want 4200", got)
	}

	// cus_b takes back all 200 GB-h it was billed for: -2500 on top of the base fee of 1000 is -1500 net and -300 tax, 1800 more than November charges.
	// cus_a takes back 30 calls: -60, which its base fee absorbs.
	e.addLate("2026-11", "2026-10", "cus_b", "gb_hours", "-200.000", 3)
	e.addLate("2026-11", "2026-10", "cus_a", "calls", "-30", 0)

	// The preview says what the invoice will.
	e.clk.t = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	preview, err := e.api().GetUpcomingInvoice(inputs.TenantCtx(context.Background(), e.tid), &billingv1.GetUpcomingInvoiceRequest{CustomerExternalId: "cus_b"})
	if err != nil {
		t.Fatal(err)
	}
	if p := preview.GetUpcoming(); p.GetTotalMinor() != 0 || p.GetCreditSurplusMinor() != 1800 || p.GetTaxMinor() != -300 {
		t.Fatalf("preview total %d surplus %d tax %d; want 0, 1800 and -300", p.GetTotalMinor(), p.GetCreditSurplusMinor(), p.GetTaxMinor())
	}

	e.clk.t = time.Date(2026, 12, 5, 12, 0, 0, 0, time.UTC)
	e.driveRun("2026-11")
	testkit.Eventually(t, 30*time.Second, "november sagas", func() bool {
		s := e.sagaStates("2026-11")
		return s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	if got := e.invoiceTotal("2026-11", "cus_a"); got != 940 {
		t.Fatalf("november cus_a total = %d, want 940", got)
	}
	if got := e.invoiceTotal("2026-11", "cus_b"); got != 0 {
		t.Fatalf("november cus_b total = %d, want 0", got)
	}
	lines := e.lines("2026-11", "cus_b")
	if len(lines) != 3 || lines[0].Amount != 1000 || lines[1].Amount != -2500 || lines[2].Kind != "TAX" || lines[2].Amount != -300 {
		t.Fatalf("november cus_b lines = %+v; want the base fee, the late -2500 and a TAX line of -300", lines)
	}

	// The surplus is one credit balance, in the invoice's currency, with the invoice as its source, and the invoice says so.
	var inv uuid.UUID
	if err := shared.sql.QueryRow(context.Background(), `SELECT id FROM invoices WHERE tenant_id = $1 AND period_id = '2026-11' AND snapshot->>'customer_external_id' = 'cus_b'`, e.tid).Scan(&inv); err != nil {
		t.Fatal(err)
	}
	var n int
	var amount int64
	var currency string
	if err := shared.sql.QueryRow(context.Background(), `SELECT count(*), COALESCE(sum(amount_minor), 0), COALESCE(max(currency), '') FROM credit_balances WHERE tenant_id = $1 AND source_surplus_invoice_id = $2`,
		e.tid, inv).Scan(&n, &amount, &currency); err != nil || n != 1 || amount != 1800 || currency != "USD" {
		t.Fatalf("surplus credits = %d of %d %s, %v; want one of 1800 USD", n, amount, currency, err)
	}
	got, err := e.api().GetInvoice(inputs.TenantCtx(context.Background(), e.tid), &billingv1.GetInvoiceRequest{InvoiceId: inv.String()})
	if err != nil || got.GetInvoice().GetCreditSurplusMinor() != 1800 || got.GetInvoice().GetTotalMinor() != 0 {
		t.Fatalf("invoice = %v, %v", got.GetInvoice(), err)
	}
	if e.invoiceTotal("2026-11", "cus_a") != 940 {
		t.Error("an invoice that stays positive carries no surplus")
	}
	if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM credit_balances WHERE tenant_id = $1 AND source_surplus_invoice_id IS NOT NULL`, e.tid).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d surplus credits in all, %v; want 1 (cus_a has none)", n, err)
	}

	// At most one per invoice, whatever tries to write another.
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_surplus_invoice_id, currency) VALUES ($1,$2,$3,5,$4,'USD')`,
		e.tid, e.customer("cus_b").Id, uuid.New(), inv); err == nil {
		t.Error("a second credit for the same invoice was written")
	}

	// The next invoice uses it: December is 1000 + 200 tax, paid out of the 1800.
	e.clk.t = time.Date(2027, 1, 5, 12, 0, 0, 0, time.UTC)
	e.driveRun("2026-12")
	testkit.Eventually(t, 30*time.Second, "december sagas", func() bool { return e.sagaStates("2026-12")["cus_b"] == "COMPLETED" })
	if got := e.invoiceTotal("2026-12", "cus_b"); got != 0 {
		t.Fatalf("december cus_b total = %d, want 0 (paid by the credit)", got)
	}
	var applied int64
	if err := shared.sql.QueryRow(context.Background(), `SELECT COALESCE(sum(amount_minor), 0) FROM credit_applications WHERE tenant_id = $1 AND credit_id IN
		(SELECT id FROM credit_balances WHERE tenant_id = $1 AND source_surplus_invoice_id = $2)`, e.tid, inv).Scan(&applied); err != nil || applied != 1200 {
		t.Errorf("applied %d of the surplus, %v; want 1200 (1000 and its 200 tax)", applied, err)
	}
}
