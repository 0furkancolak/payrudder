//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"
	"time"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// A correction is taxed like the invoice it corrects: a dearer version adds tax to the correction invoice. A customer in a country without a rate owes the net amount.
func testRerateCorrectionsCarryTheTax(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	e.customer("cus_a").Country = "DE"
	e.setTax(&plansv1.TaxRate{Country: "DE", Name: "VAT", RateBps: 2000})
	completeOctober(e)

	// base 1200, gb_hours 15/unit (1.250 -> 19): cus_a owes 200 + 3, cus_b 200.
	e.rerate("2026-10", e.newVersion(1200, "15", "2"))
	rows, err := shared.system.Query(context.Background(), `SELECT i.snapshot->>'customer_external_id', i.total_minor, i.subtotal_minor, l.kind, l.amount_minor
		FROM invoices i JOIN invoice_lines l ON l.tenant_id = i.tenant_id AND l.invoice_id = i.id WHERE i.tenant_id = $1 AND i.kind = 'CORRECTION' ORDER BY 1, l.line_no`, e.tid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		cust            string
		total, subtotal int64
		kind            string
		amount          int64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.cust, &r.total, &r.subtotal, &r.kind, &r.amount); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	// 20 % of 203 is 40.6: 41.
	want := []row{{"cus_a", 244, 203, "CORRECTION", 200}, {"cus_a", 244, 203, "CORRECTION", 3}, {"cus_a", 244, 203, "TAX", 41}, {"cus_b", 200, 200, "CORRECTION", 200}}
	if len(got) != len(want) {
		t.Fatalf("correction lines = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("correction lines = %+v; want %+v", got, want)
		}
	}
}

// A cheaper version gives the tax back with the credit: the customer paid it on what is now not owed.
func testRerateCreditsGiveTheTaxBack(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	e.customer("cus_a").Country = "DE"
	e.setTax(&plansv1.TaxRate{Country: "DE", Name: "VAT", RateBps: 2000})
	completeOctober(e) // cus_a 1116 and 223 tax, cus_b 1000
	// 800 base, gb_hours 10 (12), calls 1.6 (80): cus_a is owed 224 and the 20 % tax on it (44.8: 45), cus_b 200.
	e.rerate("2026-10", e.newVersion(800, "10", "1.6"))
	if got := e.credits(); got["cus_a"] != 224+45 || got["cus_b"] != 200 {
		t.Fatalf("credits = %v; want cus_a %d (224 and its tax) and cus_b 200", got, 224+45)
	}
}

// A plan change or a change of seats that leaves the customer with credit gives the tax back too: the unused part was paid for with tax at the customer's rate,
// like the tax an upgrade adds to what it charges. A customer in a country without a rate is credited the net amount.
func testPlanChangeCreditGivesTheTaxBack(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	e.customer("cus_a").Country = "DE"
	e.setTax(&plansv1.TaxRate{Country: "DE", Name: "VAT", RateBps: 2000})
	pv := e.pv()
	e.adjustment("cus_a", pv, 800, 300) // 500 of credit, and 20 % of it is 100
	e.adjustment("cus_b", pv, 800, 300) // no country: 500
	if err := e.fees.Bill(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, total, cur := e.creditsOf("cus_a"); n != 1 || total != 600 || cur != "USD" {
		t.Errorf("taxed customer's credit = %d x %d %s; want one of 600 USD", n, total, cur)
	}
	if n, total, _ := e.creditsOf("cus_b"); n != 1 || total != 500 {
		t.Errorf("untaxed customer's credit = %d x %d; want one of 500", n, total)
	}
}

// The seats are on the line of the fee, so an invoice of 15000 says what it is for.
func testFeeInvoiceLineSaysTheSeats(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv()
	start := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	e.feeDue("cus_a", pv, plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH, start, start.AddDate(0, 1, 0), 15000, func(s *plansv1.Subscription) { s.Quantity = 5 })
	e.feeDue("cus_b", pv, plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH, start, start.AddDate(0, 1, 0), 3000, nil)
	if err := e.fees.Bill(context.Background()); err != nil {
		t.Fatal(err)
	}
	if l := e.feeInvoiceLines("cus_a"); len(l) != 1 || l[0].Amount != 15000 || !strings.HasPrefix(l[0].Description, "Base fee (monthly) × 5 seats — ") {
		t.Errorf("five seats: lines = %+v", l)
	}
	if l := e.feeInvoiceLines("cus_b"); len(l) != 1 || strings.Contains(l[0].Description, "seats") {
		t.Errorf("one seat: lines = %+v; the line does not mention seats", l)
	}
}
