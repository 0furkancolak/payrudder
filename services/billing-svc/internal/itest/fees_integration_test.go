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
