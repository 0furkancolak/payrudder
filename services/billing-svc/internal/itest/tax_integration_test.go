//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

func (e *env) setTax(rates ...*plansv1.TaxRate) {
	e.w.mu.Lock()
	defer e.w.mu.Unlock()
	e.w.taxRates = rates
}

func (e *env) invoiceAmounts(period, customer string) (subtotal, total int64) {
	e.t.Helper()
	if err := shared.system.QueryRow(context.Background(), `SELECT subtotal_minor, total_minor FROM invoices WHERE tenant_id = $1 AND period_id = $2 AND snapshot->>'customer_external_id' = $3`,
		e.tid, period, customer).Scan(&subtotal, &total); err != nil {
		e.t.Fatal(err)
	}
	return subtotal, total
}

func testTaxIsAddedOnTopOfTheInvoicesQuotifyRaises(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv() // base fee 1000
	e.customer("cus_a").Country = "DE"
	e.customer("cus_b").Country = "DE"
	e.w.customers["cus_c"] = &plansv1.Customer{Id: uuid.NewString(), ExternalId: "cus_c", Name: "Customer cus_c", Email: "cus_c@example.com", PaymentMethod: "pm_sandbox_success", Country: "BR"}
	e.w.customers["cus_d"] = &plansv1.Customer{Id: uuid.NewString(), ExternalId: "cus_d", Name: "Customer cus_d", Email: "cus_d@example.com", PaymentMethod: "pm_sandbox_success", Country: "NO"}
	e.setTax(
		&plansv1.TaxRate{Country: "DE", Name: "VAT", RateBps: 1900},
		&plansv1.TaxRate{Country: "NO", Name: "MVA", RateBps: 0},
		&plansv1.TaxRate{Name: "Sales tax", RateBps: 825}, // every other country
	)
	paddle := e.sub("cus_b", pv, func(s *plansv1.Subscription) { s.Provider, s.ProviderRef, s.Owner = "paddle", "sub_01", "provider" })
	e.setSubs(e.sub("cus_a", pv, nil), paddle, e.sub("cus_c", pv, nil), e.sub("cus_d", pv, nil))
	e.addUsage("2026-10", "cus_a", "calls", "150", 0) // base 1000 + (150-100) × 2 = 1100
	e.addUsage("2026-10", "cus_b", "calls", "150", 0) // a provider-run subscription: usage 100 only

	e.driveRun("2026-10")

	// 19 % of 1100 is 209; the subtotal is before tax and the total after it.
	if l := e.lines("2026-10", "cus_a"); len(l) != 3 || l[2].Kind != "TAX" || l[2].Amount != 209 || l[2].Description != "VAT 19%" {
		t.Errorf("DE lines = %+v", l)
	}
	if sub, total := e.invoiceAmounts("2026-10", "cus_a"); sub != 1100 || total != 1309 {
		t.Errorf("DE subtotal %d total %d; want 1100 and 1309", sub, total)
	}
	// What a provider charges for, the provider taxes.
	for _, l := range e.lines("2026-10", "cus_b") {
		if l.Kind == "TAX" {
			t.Errorf("a provider-run subscription's invoice carries tax: %+v", l)
		}
	}
	// A country without a rate of its own gets the default: 8.25 % of 1000 is 82.5, half-even 82.
	if l := e.lines("2026-10", "cus_c"); len(l) != 2 || l[1].Kind != "TAX" || l[1].Amount != 82 || l[1].Description != "Sales tax 8.25%" {
		t.Errorf("BR lines = %+v", l)
	}
	// A rate of 0 adds no line.
	if l := e.lines("2026-10", "cus_d"); len(l) != 1 {
		t.Errorf("NO lines = %+v; a zero rate adds nothing", l)
	}
	if sub, total := e.invoiceAmounts("2026-10", "cus_d"); sub != 1000 || total != 1000 {
		t.Errorf("NO subtotal %d total %d", sub, total)
	}
}

func testFeeInvoiceTaxIsOnTheDiscountedFeeNotOnCredit(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv()
	e.customer("cus_a").Country = "DE"
	e.setTax(&plansv1.TaxRate{Country: "DE", Name: "VAT", RateBps: 2000})
	// A credit of 1000 waits for the customer.
	if _, err := shared.system.Exec(context.Background(), `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_adjustment_id, currency) VALUES ($1,$2,$3,1000,$4,'USD')`,
		e.tid, e.customer("cus_a").Id, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	start, end := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC)
	d := e.feeDue("cus_a", pv, plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH, start, end, 10000, nil)
	d.DiscountMinor, d.CouponCode = 2500, "WELCOME"
	if err := e.mgr.BillFees(context.Background()); err != nil {
		t.Fatal(err)
	}
	// (10000 - 2500) × 20 % = 1500 tax; total 9000, less the credit of 1000.
	l := e.feeInvoiceLines("cus_a")
	if len(l) != 4 || l[2].Kind != "TAX" || l[2].Amount != 1500 || l[3].Kind != "CREDIT" || l[3].Amount != -1000 {
		t.Fatalf("lines = %+v", l)
	}
	var sub, total int64
	if err := shared.system.QueryRow(context.Background(), `SELECT subtotal_minor, total_minor FROM invoices WHERE tenant_id = $1 AND kind = 'FEE'`, e.tid).Scan(&sub, &total); err != nil {
		t.Fatal(err)
	}
	if sub != 6500 || total != 8000 {
		t.Errorf("subtotal %d total %d; want 6500 (7500 less the credit) and 8000", sub, total)
	}
	// The DISCOUNT line names its coupon, which is what the accounting report groups by.
	var coupon *string
	if err := shared.system.QueryRow(context.Background(), `SELECT coupon_code FROM invoice_lines WHERE tenant_id = $1 AND kind = 'DISCOUNT'`, e.tid).Scan(&coupon); err != nil || coupon == nil || *coupon != "WELCOME" {
		t.Errorf("coupon of the discount line = %v, %v", coupon, err)
	}
}
