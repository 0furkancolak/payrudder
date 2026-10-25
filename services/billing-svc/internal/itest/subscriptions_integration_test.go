//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

func ts(y int, m time.Month, d int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
}

type lineRow struct {
	Kind        string
	Description string
	Amount      int64
}

func (e *env) lines(period, customer string) []lineRow {
	e.t.Helper()
	rows, err := shared.system.Query(context.Background(), `SELECT l.kind, l.description, l.amount_minor FROM invoice_lines l
		JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
		WHERE i.tenant_id = $1 AND i.period_id = $2 AND i.snapshot->>'customer_external_id' = $3 ORDER BY l.line_no`, e.tid, period, customer)
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

func (e *env) invoiceCount(period, customer string) int {
	e.t.Helper()
	var n int
	if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE tenant_id = $1 AND period_id = $2 AND snapshot->>'customer_external_id' = $3`,
		e.tid, period, customer).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) setSubs(subs ...*plansv1.Subscription) {
	e.w.mu.Lock()
	defer e.w.mu.Unlock()
	e.w.subs = subs
}

func (e *env) sub(customer string, pv *plansv1.PlanVersion, mutate func(*plansv1.Subscription)) *plansv1.Subscription {
	c := e.customer(customer)
	s := &plansv1.Subscription{Id: uuid.NewString(), CustomerId: c.Id, CustomerExternalId: customer, PlanVersionId: pv.Id, StartTime: ts(2026, 1, 1)}
	if mutate != nil {
		mutate(s)
	}
	return s
}

func testPlanChangeSplitsTheFixedFee(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	basic := e.pv() // base fee 1000
	pro := e.w.versions[e.newVersion(3000, "12.5", "2").String()]
	first := e.sub("cus_a", basic, nil)
	second := e.sub("cus_a", pro, func(s *plansv1.Subscription) { s.StartTime = ts(2026, 10, 16); s.ChangedFromId = first.Id })
	first.EndTime = ts(2026, 10, 16)
	e.setSubs(first, second, e.sub("cus_b", basic, nil))

	e.driveRun("2026-10")

	// October has 31 days: 15 days on basic (1000) and 16 on pro (3000).
	got := e.lines("2026-10", "cus_a")
	if len(got) != 2 || got[0].Kind != "BASE_FEE" || got[1].Kind != "BASE_FEE" || got[0].Amount != 484 || got[1].Amount != 1548 {
		t.Fatalf("cus_a lines = %+v; want two base fee lines 484 and 1548", got)
	}
	if got[0].Description != "Base fee — pro — 2026-10-01 to 2026-10-16" && got[0].Description != "Base fee — basic — 2026-10-01 to 2026-10-16" {
		t.Errorf("first description = %q", got[0].Description)
	}
	if total := e.invoiceTotal("2026-10", "cus_a"); total != 2032 {
		t.Errorf("cus_a total = %d; want 2032", total)
	}
	// A customer who never changed plan sees the plain base fee.
	if l := e.lines("2026-10", "cus_b"); len(l) != 1 || l[0].Description != "Base fee" || l[0].Amount != 1000 {
		t.Errorf("cus_b lines = %+v", l)
	}
}

func testTrialAndPauseWaiveTheFixedFee(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv()
	e.w.customers["cus_c"] = &plansv1.Customer{Id: uuid.NewString(), ExternalId: "cus_c", Name: "Customer cus_c", Email: "cus_c@example.com", PaymentMethod: "pm_sandbox_success"}
	trialing := e.sub("cus_a", pv, func(s *plansv1.Subscription) { s.TrialEndTime = ts(2026, 11, 10) })  // still on trial when October closes
	trialOver := e.sub("cus_c", pv, func(s *plansv1.Subscription) { s.TrialEndTime = ts(2026, 10, 10) }) // trial ended in October
	paused := e.sub("cus_b", pv, func(s *plansv1.Subscription) {
		s.State = plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED
		s.PauseTime = ts(2026, 9, 20)
	})
	e.setSubs(trialing, trialOver, paused)
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.250", 3) // 15.625 → 16
	e.addUsage("2026-10", "cus_b", "calls", "150", 0)      // (150-100) × 2 = 100

	e.driveRun("2026-10")

	if total := e.invoiceTotal("2026-10", "cus_a"); total != 16 {
		t.Errorf("trialing customer total = %d; want usage only (16)", total)
	}
	if total := e.invoiceTotal("2026-10", "cus_b"); total != 100 {
		t.Errorf("paused customer total = %d; want usage only (100)", total)
	}
	if total := e.invoiceTotal("2026-10", "cus_c"); total != 1000 {
		t.Errorf("customer whose trial ended in the period total = %d; want the base fee (1000)", total)
	}
	for _, l := range e.lines("2026-10", "cus_a") {
		if l.Kind == "BASE_FEE" {
			t.Errorf("trialing customer got a base fee line: %+v", l)
		}
	}
}

func testSubscriptionInAnotherCurrencyIsInvoicedInIt(t *testing.T) {
	e := newEnv(t, envOpts{}) // the tenant bills in USD
	e.startWorkers(nil)
	base := e.pv()
	euro := &plansv1.PlanVersion{Id: uuid.NewString(), PlanCode: "pro", Version: 3, BaseFeeMinor: 1000, Prices: base.Prices,
		Fees: []*plansv1.CurrencyFee{{Currency: "EUR", BaseFeeMinor: 800}},
		CurrencyPrices: []*plansv1.CurrencyPrices{{Currency: "EUR", Prices: []*plansv1.Price{
			{MeterCode: "gb_hours", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "10"}}},
			{MeterCode: "calls", Model: &plansv1.Price_Tiered{Tiered: &plansv1.TieredGraduatedPrice{Tiers: []*plansv1.PriceTier{
				{UpTo: "100", UnitPrice: "0"}, {UnitPrice: "1"}}}}},
		}}}}
	e.w.mu.Lock()
	e.w.versions[euro.Id] = euro
	e.w.mu.Unlock()
	e.setSubs(e.sub("cus_a", euro, func(s *plansv1.Subscription) { s.Currency = "EUR" }), e.sub("cus_b", base, nil))
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.250", 3) // 12.5 → 12 (half-even)
	e.addUsage("2026-10", "cus_a", "calls", "150", 0)      // 50 × 1

	e.driveRun("2026-10")

	if total := e.invoiceTotal("2026-10", "cus_a"); total != 800+12+50 {
		t.Errorf("EUR customer total = %d; want 862 (800 fee + 12 + 50 usage in euro prices)", total)
	}
	var currency string
	var exponent int
	if err := shared.system.QueryRow(context.Background(), `SELECT currency, (snapshot->>'currency_exponent')::int FROM invoices
		WHERE tenant_id = $1 AND period_id = '2026-10' AND snapshot->>'customer_external_id' = 'cus_a'`, e.tid).Scan(&currency, &exponent); err != nil {
		t.Fatal(err)
	}
	if currency != "EUR" || exponent != 2 {
		t.Errorf("invoice currency %q exponent %d; want EUR 2", currency, exponent)
	}
	if err := shared.system.QueryRow(context.Background(), `SELECT currency FROM invoices WHERE tenant_id = $1 AND period_id = '2026-10' AND snapshot->>'customer_external_id' = 'cus_b'`, e.tid).Scan(&currency); err != nil || currency != "USD" {
		t.Errorf("tenant-currency customer invoice currency %q, %v; want USD", currency, err)
	}
}

func testSubscriptionWithoutAFeeInItsCurrencyFailsPermanently(t *testing.T) {
	e := newEnv(t, envOpts{})
	base := e.pv() // no EUR fee
	e.setSubs(e.sub("cus_a", base, func(s *plansv1.Subscription) { s.Currency = "EUR" }))
	e.addUsage("2026-10", "cus_a", "calls", "150", 0)
	run := e.driveRun("2026-10")
	if run.Status != "FAILED" {
		t.Fatalf("run = %s (%s); want FAILED: the plan version has no fee in EUR", run.Status, run.LastError)
	}
	if e.invoiceCount("2026-10", "cus_a") != 0 {
		t.Error("an invoice was created in a currency the plan cannot be sold in")
	}
}

func testProviderOwnedSubscriptionBillsOnlyUsageThroughItsProvider(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(nil)
	pv := e.pv() // base fee 1000
	owned := e.sub("cus_a", pv, func(s *plansv1.Subscription) { s.Provider, s.ProviderRef, s.Owner = "paddle", "sub_01", "provider" })
	e.setSubs(owned, e.sub("cus_b", pv, nil))
	e.addUsage("2026-10", "cus_a", "calls", "150", 0) // (150-100) × 2 = 100

	e.driveRun("2026-10")

	// Paddle bills the fixed fee on its own renewal; the Payrudder invoice carries the usage only.
	got := e.lines("2026-10", "cus_a")
	if len(got) != 1 || got[0].Kind != "USAGE" || got[0].Amount != 100 {
		t.Fatalf("provider-owned lines = %+v; want the usage only", got)
	}
	if l := e.lines("2026-10", "cus_b"); len(l) != 1 || l[0].Kind != "BASE_FEE" || l[0].Amount != 1000 {
		t.Errorf("a Payrudder-owned customer still pays its fixed fee: %+v", l)
	}
	testkit.Eventually(t, 30*time.Second, "the charge command for the provider-owned invoice", func() bool {
		for _, c := range stub.Commands() {
			if ch := c.GetChargePayment(); ch != nil && c.GetTenantId() == e.tid.String() && ch.GetAmountMinor() == 100 {
				if ch.GetProvider() != "paddle" || ch.GetPaymentMethod() != "sub_01" {
					t.Fatalf("charge = provider %q payment method %q; want paddle and the provider subscription", ch.GetProvider(), ch.GetPaymentMethod())
				}
				return true
			}
		}
		return false
	})
}
