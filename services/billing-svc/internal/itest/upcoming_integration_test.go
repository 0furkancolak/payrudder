//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
)

// The preview of an open period is the invoice the run drafts for it when the same inputs are there at the close: line for line, to the minor unit. One customer has
// everything that moves a total (usage of a fractional meter above a free tier, late usage of an earlier period, tax, a credit balance), and the preview is read well before
// the period ends.
func testUpcomingInvoiceIsTheInvoiceTheRunWouldDraft(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv() // base fee 1000, gb_hours 12.5 a unit (scale 3), calls 100 free then 2
	e.customer("cus_a").Country = "DE"
	e.setTax(&plansv1.TaxRate{Country: "DE", Name: "VAT", RateBps: 1900})
	e.setSubs(e.sub("cus_a", pv, nil), e.sub("cus_b", pv, nil))
	e.addUsage("2026-10", "cus_a", "gb_hours", "2.505", 3) // 31.3125 -> 31
	e.addUsage("2026-10", "cus_a", "calls", "150", 0)      // (150-100) x 2 = 100
	e.addUsage("2026-09", "cus_a", "calls", "90", 0)
	e.addLate("2026-10", "2026-09", "cus_a", "calls", "30", 0) // September's 90 become 120: 40 more, billed in October
	lot := uuid.New()
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_adjustment_id, currency) VALUES ($1,$2,$3,300,$4,'USD')`,
		e.tid, e.customer("cus_a").Id, lot, uuid.New()); err != nil {
		t.Fatal(err)
	}

	e.clk.t = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	svc := e.api()
	resp, err := svc.GetUpcomingInvoice(inputs.TenantCtx(context.Background(), e.tid), &billingv1.GetUpcomingInvoiceRequest{CustomerExternalId: "cus_a"})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.GetUpcoming()
	if !got.Estimate || !got.HasInvoice || got.PeriodId != "2026-10" || got.Currency != "USD" || !got.AsOf.AsTime().Equal(e.clk.t) {
		t.Fatalf("preview header = %+v", got)
	}
	if n := invoiceCount(t, e); n != 0 {
		t.Fatalf("the preview wrote %d invoices", n)
	}

	// The period closes: the run drafts the real invoice from the same inputs.
	e.clk.t = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	e.driveRun("2026-10")
	testkit.Eventually(t, 30*time.Second, "the sagas to complete", func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	inv, err := svc.GetInvoice(inputs.TenantCtx(context.Background(), e.tid), &billingv1.GetInvoiceRequest{InvoiceId: e.invoiceIDOf("cus_a").String()})
	if err != nil {
		t.Fatal(err)
	}
	real := inv.GetInvoice()
	if len(got.Lines) != len(real.Lines) || len(real.Lines) < 6 {
		t.Fatalf("preview has %d lines, the invoice %d (want base fee, two usage lines, late usage, tax, credit): %+v", len(got.Lines), len(real.Lines), got.Lines)
	}
	for i, l := range real.Lines {
		p := got.Lines[i]
		if p.Kind != l.Kind || p.Description != l.Description || p.AmountMinor != l.AmountMinor || p.Quantity != l.Quantity || p.MeterCode != l.MeterCode || p.OriginPeriodId != l.OriginPeriodId {
			t.Errorf("line %d: preview %+v, invoice %+v", i+1, p, l)
		}
	}
	if got.SubtotalMinor != real.SubtotalMinor || got.TotalMinor != real.TotalMinor {
		t.Errorf("preview subtotal %d total %d, invoice subtotal %d total %d", got.SubtotalMinor, got.TotalMinor, real.SubtotalMinor, real.TotalMinor)
	}
	// 1000 + 31 + 100 + 40 = 1171; VAT 19% = 222.49 -> 222 (the credit is not taxed away); credit 300; 1171 + 222 - 300.
	if got.TaxMinor != 222 || got.CreditMinor != 300 || got.TotalMinor != 1093 {
		t.Errorf("tax %d credit %d total %d; want 222, 300 and 1093", got.TaxMinor, got.CreditMinor, got.TotalMinor)
	}
}

// A preview consumes no credit, creates no invoice or run, and a customer that does not exist is a not-found with its key; a customer with nothing to bill has no invoice.
func testUpcomingInvoiceWritesNothingAndRefusesAStranger(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.setSubs(e.sub("cus_a", e.pv(), nil)) // cus_b has no subscription
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_adjustment_id, currency) VALUES ($1,$2,$3,5000,$4,'USD')`,
		e.tid, e.customer("cus_a").Id, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	e.clk.t = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	svc := e.api()
	ctx := inputs.TenantCtx(context.Background(), e.tid)

	// 5000 of credit is more than the invoice (1000): the preview takes all of the invoice and leaves the balance whole.
	preview, err := svc.GetUpcomingInvoice(ctx, &billingv1.GetUpcomingInvoiceRequest{CustomerExternalId: "cus_a"})
	got := preview.GetUpcoming()
	if err != nil || got.TotalMinor != 0 || got.CreditMinor != 1000 || got.SubtotalMinor != 0 {
		t.Fatalf("preview = %+v, %v; want a credit of 1000 and nothing to pay", got, err)
	}
	for _, table := range []string{"credit_applications", "invoices", "billing_runs", "invoice_sagas"} {
		var n int
		if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, e.tid).Scan(&n); err != nil || n != 0 {
			t.Errorf("the preview left %d rows in %s, %v", n, table, err)
		}
	}

	noneResp, err := svc.GetUpcomingInvoice(ctx, &billingv1.GetUpcomingInvoiceRequest{CustomerExternalId: "cus_b"})
	none := noneResp.GetUpcoming()
	if err != nil || none.HasInvoice || len(none.Lines) != 0 || none.TotalMinor != 0 || !none.Estimate {
		t.Errorf("a customer without a subscription: %+v, %v; want no invoice", none, err)
	}
	if _, err := svc.GetUpcomingInvoice(ctx, &billingv1.GetUpcomingInvoiceRequest{CustomerExternalId: "nobody"}); status.Code(err) != codes.NotFound {
		t.Errorf("an unknown customer: %v, want NOT_FOUND", err)
	}
}

func invoiceCount(t *testing.T, e *env) int {
	t.Helper()
	var n int
	if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE tenant_id = $1`, e.tid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
