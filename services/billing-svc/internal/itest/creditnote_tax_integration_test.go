//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// taxedOctober bills October to a German customer at 20 % VAT: 1000 base fee + 50 calls over the free hundred × 2 = 1100, tax 220, total 1320 (cus_b gets the base fee only).
func taxedOctober(e *env) {
	e.t.Helper()
	e.customer("cus_a").Country = "DE"
	e.setTax(&plansv1.TaxRate{Country: "DE", Name: "VAT", RateBps: 2000})
	e.addUsage("2026-10", "cus_a", "calls", "150", 0)
	e.driveRun("2026-10")
	testkit.Eventually(e.t, 30*time.Second, "both sagas to COMPLETE", func() bool {
		s := e.sagaStates("2026-10")
		return len(s) == 2 && s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	if sub, total := e.invoiceAmounts("2026-10", "cus_a"); sub != 1100 || total != 1320 {
		e.t.Fatalf("subtotal %d total %d; want 1100 and 1320", sub, total)
	}
}

// A credit note gives back the tax of what it credits, in proportion; the notes of one invoice add up to its tax; the e-document of a note carries that tax on its TAX line;
// the accounting summary takes the tax off its tax figure (docs/code-quality/billing-svc-status.md, "credit-note tax is not reversed").
func testCreditNotesGiveBackTheTaxInProportion(t *testing.T) {
	e := einvoiceEnv(t, "finalized")
	stub := e.startWorkers(nil)
	taxedOctober(e)
	svc := e.api()
	op := tenant.WithOperator(context.Background())
	inv := invoiceID(t, e, "cus_a")
	var gave int64
	for i, tc := range []struct{ amount, wantTotal, wantTax int64 }{
		{400, 400, 67}, // 400 of 1320 is 30.3 % of 220 = 66.67
		{400, 400, 67}, // the same again
		{0, 520, 86},   // what is left of the invoice takes what is left of the tax: 220 - 134, not 520 × 220 / 1320 = 86.67
	} {
		e.clk.Advance(time.Second)
		r, err := svc.CreateCreditNote(op, &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: inv, AmountMinor: tc.amount, Reason: "tax"})
		if err != nil {
			t.Fatalf("note %d: %v", i+1, err)
		}
		cn := r.GetCreditNote()
		if cn.GetTotalMinor() != tc.wantTotal || cn.GetTaxMinor() != tc.wantTax {
			t.Fatalf("note %d: total %d tax %d; want %d and %d", i+1, cn.GetTotalMinor(), cn.GetTaxMinor(), tc.wantTotal, tc.wantTax)
		}
		gave += cn.GetTaxMinor()
	}
	if gave != 220 {
		t.Errorf("the notes gave back %d of the invoice's 220 tax", gave)
	}
	var stored int64
	if err := shared.sql.QueryRow(context.Background(), `SELECT sum(tax_minor) FROM credit_notes WHERE tenant_id = $1`, e.tid).Scan(&stored); err != nil || stored != 220 {
		t.Errorf("stored tax of the notes = %d, %v", stored, err)
	}
	// The list and the event say it too.
	tctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})
	list, err := svc.ListCreditNotes(tctx, &billingv1.ListCreditNotesRequest{InvoiceId: inv})
	if err != nil || len(list.GetCreditNotes()) != 3 || list.GetCreditNotes()[0].GetTaxMinor() != 86 {
		t.Errorf("list = %v, %v", list, err)
	}

	// Their e-documents: the TAX line is the tax the note gave back and the other lines carry the rest.
	testkit.Eventually(t, 20*time.Second, "the notes e-invoiced", allIssued(t, e.tid, 5))
	var seen int
	for _, c := range stub.Commands() {
		x := c.GetIssueEInvoice()
		if x == nil || x.GetKind() != "credit_note" {
			continue
		}
		seen++
		var tax, rest int64
		for _, l := range x.GetDocument().GetLines() {
			if l.GetKind() == "TAX" {
				tax += l.GetAmountMinor()
			} else {
				rest += l.GetAmountMinor()
			}
		}
		want := map[int64]int64{400: 67, 520: 86}[x.GetDocument().GetTotalMinor()]
		if tax != want || tax+rest != x.GetDocument().GetTotalMinor() {
			t.Errorf("credit note document %s: tax %d and %d besides, total %d; want tax %d", x.GetDocument().GetNumber(), tax, rest, x.GetDocument().GetTotalMinor(), want)
		}
	}
	if seen != 3 {
		t.Errorf("%d credit note documents, want 3", seen)
	}

	// Accounting: 1320 + 1000 paid with 220 tax, 1320 credited with 220 tax: the tax kept is nil for cus_a and the net of cus_a is nothing earned.
	sum, err := svc.GetAccountingSummary(tctx, &billingv1.GetAccountingSummaryRequest{FromDate: "2026-01-01", ToDate: "2027-12-31"})
	if err != nil || len(sum.GetCurrencies()) != 1 {
		t.Fatalf("summary = %v, %v", sum, err)
	}
	c := sum.GetCurrencies()[0]
	if c.GetGrossIncomeMinor() != 2320 || c.GetTaxMinor() != 0 || c.GetRefundsMinor() != 1320 || c.GetNetIncomeMinor() != 1000 {
		t.Errorf("accounting = gross %d tax %d refunds %d net %d; want 2320, 0, 1320 and 1000", c.GetGrossIncomeMinor(), c.GetTaxMinor(), c.GetRefundsMinor(), c.GetNetIncomeMinor())
	}
}
