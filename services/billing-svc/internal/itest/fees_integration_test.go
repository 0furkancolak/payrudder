//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

func (e *env) feeDue(customer string, pv *plansv1.PlanVersion, interval plansv1.SubscriptionInterval, start, end time.Time, fee int64, mutate func(*plansv1.Subscription)) *plansv1.FeeDue {
	s := e.sub(customer, pv, func(s *plansv1.Subscription) {
		s.Interval = interval
		s.FeeTiming = plansv1.FeeTiming_FEE_TIMING_ADVANCE
		s.StartTime = timestamppb.New(start)
		s.Currency = "USD"
		s.PlanCode = pv.PlanCode
		if mutate != nil {
			mutate(s)
		}
	})
	d := &plansv1.FeeDue{Subscription: s, PeriodStart: timestamppb.New(start), PeriodEnd: timestamppb.New(end), FeeMinor: fee}
	e.w.mu.Lock()
	e.w.feeDue = append(e.w.feeDue, d)
	e.w.mu.Unlock()
	return d
}

func (e *env) feeInvoices(customer string) (n int, total int64, desc string) {
	e.t.Helper()
	rows, err := shared.system.Query(context.Background(), `SELECT i.total_minor, COALESCE(l.description, '') FROM invoices i
		LEFT JOIN invoice_lines l ON l.tenant_id = i.tenant_id AND l.invoice_id = i.id AND l.line_no = 1
		WHERE i.tenant_id = $1 AND i.kind = 'FEE' AND i.snapshot->>'customer_external_id' = $2`, e.tid, customer)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		n++
		if err := rows.Scan(&total, &desc); err != nil {
			e.t.Fatal(err)
		}
	}
	return n, total, desc
}

func testAdvanceFeesAreInvoicedOncePerPeriod(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv()
	e.w.customers["cus_c"] = &plansv1.Customer{Id: uuid.NewString(), ExternalId: "cus_c", Name: "Customer cus_c", Email: "cus_c@example.com", PaymentMethod: "pm_sandbox_success"}
	e.w.customers["cus_d"] = &plansv1.Customer{Id: uuid.NewString(), ExternalId: "cus_d", Name: "Customer cus_d", Email: "cus_d@example.com", PaymentMethod: "pm_sandbox_success"}
	nov3 := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	month, year := plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH, plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	e.feeDue("cus_a", pv, month, nov3, time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC), 1000, nil)
	e.feeDue("cus_b", pv, year, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 11, 1, 0, 0, 0, 0, time.UTC), 9000, nil)
	e.feeDue("cus_c", pv, month, nov3, time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC), 1000, func(s *plansv1.Subscription) { s.State = plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED })
	e.feeDue("cus_d", pv, month, nov3, time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC), 0, nil)

	if err := e.mgr.BillFees(context.Background()); err != nil {
		t.Fatal(err)
	}

	if n, total, desc := e.feeInvoices("cus_a"); n != 1 || total != 1000 || desc != "Base fee (monthly) — pro — 2026-11-03 to 2026-12-03" {
		t.Errorf("monthly: %d invoice(s), total %d, line %q", n, total, desc)
	}
	if n, total, desc := e.feeInvoices("cus_b"); n != 1 || total != 9000 || desc != "Base fee (yearly) — pro — 2026-11-01 to 2027-11-01" {
		t.Errorf("yearly: %d invoice(s), total %d, line %q", n, total, desc)
	}
	if n, _, _ := e.feeInvoices("cus_c"); n != 0 {
		t.Errorf("a paused subscription was invoiced %d time(s)", n)
	}
	if n, _, _ := e.feeInvoices("cus_d"); n != 0 {
		t.Errorf("a free period was invoiced %d time(s)", n)
	}
	// Paused and free periods are still moved on, otherwise they would be offered forever.
	e.w.mu.Lock()
	advanced := len(e.w.advanced)
	e.w.mu.Unlock()
	if advanced != 4 {
		t.Errorf("advanced %d fee periods; want 4", advanced)
	}

	// The invoices are charged: their sagas complete.
	eventually(t, 20*time.Second, "fee invoices to be paid", func() bool {
		var open int
		_ = shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
			WHERE i.tenant_id = $1 AND i.kind = 'FEE' AND (s.state IS NULL OR s.state <> 'COMPLETED')`, e.tid).Scan(&open)
		return open == 0
	})

	// A pass that could not advance the periods (the previous one crashed) must not invoice them again.
	e.w.mu.Lock()
	e.w.advanced = nil
	e.w.mu.Unlock()
	if err := e.mgr.BillFees(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := e.feeInvoices("cus_a"); n != 1 {
		t.Errorf("a repeated pass made %d invoices for the same period", n)
	}
	var numbers int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(DISTINCT number) FROM invoices WHERE tenant_id = $1 AND kind = 'FEE'`, e.tid).Scan(&numbers); err != nil || numbers != 2 {
		t.Errorf("fee invoices have %d distinct numbers (err %v); want 2", numbers, err)
	}
}

func testAdvanceBilledSubscriptionPaysOnlyUsageAtMonthClose(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv()
	advance := e.sub("cus_a", pv, func(s *plansv1.Subscription) { s.FeeTiming = plansv1.FeeTiming_FEE_TIMING_ADVANCE })
	e.setSubs(advance, e.sub("cus_b", pv, nil))
	e.addUsage("2026-10", "cus_a", "calls", "150", 0) // (150-100) × 2 = 100
	e.addUsage("2026-10", "cus_b", "calls", "150", 0)

	e.driveRun("2026-10")

	if total := e.invoiceTotal("2026-10", "cus_a"); total != 100 {
		t.Errorf("advance-billed customer's month invoice = %d; want the usage only (100)", total)
	}
	for _, l := range e.lines("2026-10", "cus_a") {
		if l.Kind == "BASE_FEE" {
			t.Errorf("the month invoice carries the fee that was invoiced in advance: %+v", l)
		}
	}
	if total := e.invoiceTotal("2026-10", "cus_b"); total != 1100 {
		t.Errorf("arrears customer's invoice = %d; want fee and usage (1100)", total)
	}
}

func (e *env) adjustment(customer string, pv *plansv1.PlanVersion, credit, charge int64) *plansv1.Adjustment {
	s := e.sub(customer, pv, func(s *plansv1.Subscription) {
		s.Interval = plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
		s.FeeTiming = plansv1.FeeTiming_FEE_TIMING_ADVANCE
		s.Currency = "USD"
		s.PlanCode = "pro"
	})
	a := &plansv1.Adjustment{Id: uuid.NewString(), Subscription: s, CreditMinor: credit, ChargeMinor: charge, OldPlanCode: "basic",
		FromTime: timestamppb.New(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)), ToTime: timestamppb.New(time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC))}
	e.w.mu.Lock()
	e.w.adjusts = append(e.w.adjusts, a)
	e.w.mu.Unlock()
	return a
}

func (e *env) feeInvoiceLines(customer string) []lineRow {
	e.t.Helper()
	rows, err := shared.system.Query(context.Background(), `SELECT l.kind, l.description, l.amount_minor FROM invoice_lines l
		JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
		WHERE i.tenant_id = $1 AND i.kind = 'FEE' AND i.snapshot->>'customer_external_id' = $2 ORDER BY i.created_at, l.line_no`, e.tid, customer)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []lineRow
	for rows.Next() {
		var l lineRow
		if err := rows.Scan(&l.Kind, &l.Description, &l.Amount); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, l)
	}
	return out
}

func (e *env) creditsOf(customer string) (n int, total int64, currency string) {
	e.t.Helper()
	err := shared.system.QueryRow(context.Background(), `SELECT count(*), COALESCE(sum(amount_minor), 0), COALESCE(max(currency), '') FROM credit_balances c
		WHERE c.tenant_id = $1 AND c.customer_id = $2::uuid`, e.tid, e.customer(customer).Id).Scan(&n, &total, &currency)
	if err != nil {
		e.t.Fatal(err)
	}
	return n, total, currency
}

func testPlanChangesAreInvoicedOrCredited(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv()
	e.w.customers["cus_c"] = &plansv1.Customer{Id: uuid.NewString(), ExternalId: "cus_c", Name: "Customer cus_c", Email: "cus_c@example.com", PaymentMethod: "pm_sandbox_success"}
	up := e.adjustment("cus_a", pv, 1600, 4800)   // an upgrade: 3200 to pay
	down := e.adjustment("cus_b", pv, 4800, 1600) // a downgrade now: 3200 of credit
	e.adjustment("cus_c", pv, 1000, 1000)         // moves nothing
	pass := func() {
		t.Helper()
		if err := e.mgr.BillFees(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	pass()

	if l := e.feeInvoiceLines("cus_a"); len(l) != 2 || l[0].Kind != "BASE_FEE" || l[0].Amount != 4800 || l[1].Kind != "DISCOUNT" || l[1].Amount != -1600 ||
		l[0].Description != "Base fee (monthly) — pro — 2026-11-03 to 2026-12-03" || l[1].Description != "Unused time on basic — 2026-11-03 to 2026-12-03" {
		t.Errorf("upgrade invoice lines = %+v", l)
	}
	if n, total, _ := e.feeInvoices("cus_a"); n != 1 || total != 3200 {
		t.Errorf("upgrade: %d invoice(s) totalling %d; want one of 3200", n, total)
	}
	if n, _, _ := e.feeInvoices("cus_b"); n != 0 {
		t.Errorf("a downgrade made %d invoice(s)", n)
	}
	if n, total, cur := e.creditsOf("cus_b"); n != 1 || total != 3200 || cur != "USD" {
		t.Errorf("downgrade credit = %d × %d %s; want one of 3200 USD", n, total, cur)
	}
	if n, _, _ := e.feeInvoices("cus_c"); n != 0 {
		t.Errorf("a change that moved nothing made %d invoice(s)", n)
	}
	e.w.mu.Lock()
	billed, applyCalls := len(e.w.billedAdj), e.w.applyCall
	e.w.mu.Unlock()
	if billed != 3 || applyCalls < 1 {
		t.Errorf("%d adjustments marked billed, %d apply calls; want 3 and at least 1", billed, applyCalls)
	}

	// A pass that could not mark them billed (the previous one crashed) must neither invoice nor credit twice.
	e.w.mu.Lock()
	e.w.billedAdj = nil
	e.w.mu.Unlock()
	pass()
	if n, _, _ := e.feeInvoices("cus_a"); n != 1 {
		t.Errorf("a repeated pass made %d upgrade invoices", n)
	}
	if n, total, _ := e.creditsOf("cus_b"); n != 1 || total != 3200 {
		t.Errorf("a repeated pass left %d credits totalling %d", n, total)
	}
	_ = up
	_ = down

	// The credit pays the customer's next fee, in the same currency.
	nov := time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC)
	e.feeDue("cus_b", pv, plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH, nov, time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC), 5000, nil)
	pass()
	l := e.feeInvoiceLines("cus_b")
	if len(l) != 2 || l[0].Amount != 5000 || l[1].Kind != "CREDIT" || l[1].Amount != -3200 {
		t.Fatalf("fee invoice lines with the credit applied = %+v", l)
	}
	if n, total, _ := e.feeInvoices("cus_b"); n != 1 || total != 1800 {
		t.Errorf("fee after credit: %d invoice(s) totalling %d; want one of 1800", n, total)
	}
}

func testFeeInvoiceCarriesTheCouponAndServicePeriod(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv()
	start, end := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC)
	d := e.feeDue("cus_a", pv, plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH, start, end, 10000, nil)
	d.DiscountMinor, d.CouponCode = 2500, "WELCOME"
	if err := e.mgr.BillFees(context.Background()); err != nil {
		t.Fatal(err)
	}
	l := e.feeInvoiceLines("cus_a")
	if len(l) != 2 || l[0].Kind != "BASE_FEE" || l[0].Amount != 10000 || l[1].Kind != "DISCOUNT" || l[1].Amount != -2500 || l[1].Description != "Coupon WELCOME" {
		t.Fatalf("lines = %+v", l)
	}
	if n, total, _ := e.feeInvoices("cus_a"); n != 1 || total != 7500 {
		t.Errorf("%d invoice(s) totalling %d; want one of 7500", n, total)
	}
	var withPeriod int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoice_lines l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
		WHERE i.tenant_id = $1 AND i.kind = 'FEE' AND l.service_from = $2 AND l.service_to = $3`, e.tid, start, end).Scan(&withPeriod); err != nil || withPeriod != 2 {
		t.Errorf("%d line(s) carry the service period (err %v); want 2", withPeriod, err)
	}
}
