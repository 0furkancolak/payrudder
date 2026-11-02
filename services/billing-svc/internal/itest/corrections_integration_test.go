//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// A correction of an event of a closed period reaches billing as late usage of that period with a negative quantity (ADR 0027). It is priced the way late usage always is,
// incrementally on top of what the period and the earlier late batches cost, so a cancelled event takes its own price back (tiers included); it is billed on the invoice of the
// period it was ingested in, once (the next period's invoice does not repeat it, it prices on top of it); and the finalized invoice of the origin never changes. A correction
// that takes back more than the next invoice charges costs nothing and the surplus becomes a credit balance (ANegativeTotalBecomesACreditBalance).
func testCorrectionsAreNegativeLateUsageBilledOnce(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	e.addUsage("2026-10", "cus_b", "gb_hours", "200.000", 3) // 2500
	completeOctober(e)                                       // cus_a: 1000 + 16 + (150-100) x 2 = 1116; cus_b: 1000 + 2500 = 3500
	octoberLines := countLines(t, e, "2026-10", "cus_a")

	// 30 of the 150 calls were a mistake: (120-100) x 2 - (150-100) x 2 = -60, a line of -30 calls. cus_b cancels all 200 GB-h it was billed for: -2500, more than November
	// charges it, so its total stops at zero.
	e.addLate("2026-11", "2026-10", "cus_a", "calls", "-30", 0)
	e.addLate("2026-11", "2026-10", "cus_b", "gb_hours", "-200.000", 3)
	e.clk.t = time.Date(2026, 12, 5, 12, 0, 0, 0, time.UTC)
	run := e.driveRun("2026-11")
	if run.Status != "INVOICING" {
		t.Fatalf("november run = %s (%s)", run.Status, run.LastError)
	}
	testkit.Eventually(t, 30*time.Second, "november sagas", func() bool {
		s := e.sagaStates("2026-11")
		return s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED"
	})
	if got := e.invoiceTotal("2026-11", "cus_a"); got != 940 {
		t.Fatalf("november cus_a total = %d, want 940 (base 1000, late -60)", got)
	}
	var amount int64
	var qty, origin string
	if err := shared.sql.QueryRow(context.Background(), `SELECT l.amount_minor, l.quantity::text, l.origin_period_id FROM invoice_lines l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
		WHERE i.tenant_id = $1 AND i.period_id = '2026-11' AND l.kind = 'LATE_USAGE' AND i.snapshot->>'customer_external_id' = 'cus_a'`, e.tid).Scan(&amount, &qty, &origin); err != nil || amount != -60 || qty != "-30" || origin != "2026-10" {
		t.Fatalf("late line = %d %s %s, %v; want -60 for -30 calls of 2026-10", amount, qty, origin, err)
	}
	if e.invoiceTotal("2026-10", "cus_a") != 1116 || countLines(t, e, "2026-10", "cus_a") != octoberLines {
		t.Fatal("the invoice of the corrected period changed")
	}
	// The invoice charges nothing: the line keeps its -2500 and the total is zero, and the 1500 that is left below zero is a credit balance.
	if got := e.invoiceTotal("2026-11", "cus_b"); got != 0 {
		t.Fatalf("november cus_b total = %d, want 0 (1000 - 2500 clamped)", got)
	}
	if err := shared.sql.QueryRow(context.Background(), `SELECT l.amount_minor FROM invoice_lines l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
		WHERE i.tenant_id = $1 AND i.period_id = '2026-11' AND l.kind = 'LATE_USAGE' AND i.snapshot->>'customer_external_id' = 'cus_b'`, e.tid).Scan(&amount); err != nil || amount != -2500 {
		t.Fatalf("cus_b late line = %d, %v; want -2500", amount, err)
	}
	if err := shared.sql.QueryRow(context.Background(), `SELECT COALESCE(sum(amount_minor), 0) FROM credit_balances WHERE tenant_id = $1 AND source_surplus_invoice_id IS NOT NULL`, e.tid).Scan(&amount); err != nil || amount != 1500 {
		t.Fatalf("surplus credit = %d, %v; want 1500", amount, err)
	}

	// Billed once: December has only what arrived in December (10 calls counted again), priced on top of October's 150 and the -30 already invoiced:
	// (130-100) x 2 - (120-100) x 2 = 20. The -60 is not in it.
	e.addLate("2026-12", "2026-10", "cus_a", "calls", "10", 0)
	e.clk.t = time.Date(2027, 1, 5, 12, 0, 0, 0, time.UTC)
	e.driveRun("2026-12")
	testkit.Eventually(t, 30*time.Second, "december sagas", func() bool { return e.sagaStates("2026-12")["cus_a"] == "COMPLETED" })
	if got := e.invoiceTotal("2026-12", "cus_a"); got != 1020 {
		t.Fatalf("december cus_a total = %d, want 1020 (base 1000, late +20)", got)
	}
	var lines int
	if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM invoice_lines l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
		WHERE i.tenant_id = $1 AND i.period_id = '2026-12' AND l.kind = 'LATE_USAGE' AND i.snapshot->>'customer_external_id' = 'cus_a'`, e.tid).Scan(&lines); err != nil || lines != 1 {
		t.Fatalf("december late lines = %d, %v; want 1", lines, err)
	}
}
