//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

// The fixed fee a trial, an advance-fee subscription or a provider already waived on the invoice is not billed by a re-rating either.
func testRerateKeepsTheWaivedFixedFeesWaived(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	pv := e.pv()
	e.w.customers["cus_c"] = &plansv1.Customer{Id: uuid.NewString(), ExternalId: "cus_c", Name: "Customer cus_c", Email: "cus_c@example.com", PaymentMethod: "pm_sandbox_success"}
	trialing := e.sub("cus_a", pv, func(s *plansv1.Subscription) { s.TrialEndTime = ts(2026, 11, 10) })
	advance := e.sub("cus_b", pv, func(s *plansv1.Subscription) { s.FeeTiming = plansv1.FeeTiming_FEE_TIMING_ADVANCE })
	provider := e.sub("cus_c", pv, func(s *plansv1.Subscription) { s.Provider, s.ProviderRef, s.Owner = "paddle", "sub_01", "provider" })
	e.setSubs(trialing, advance, provider)
	for _, c := range []string{"cus_a", "cus_b", "cus_c"} {
		e.addUsage("2026-10", c, "calls", "150", 0) // (150-100) × 2 = 100
	}
	e.driveRun("2026-10")
	eventually(t, 30*time.Second, "every saga to COMPLETE", func() bool {
		s := e.sagaStates("2026-10")
		return len(s) == 3 && s["cus_a"] == "COMPLETED" && s["cus_b"] == "COMPLETED" && s["cus_c"] == "COMPLETED"
	})
	if run, err := e.mgr.Drive(context.Background(), e.tid, "2026-10"); err != nil || run.Status != "COMPLETED" {
		t.Fatalf("run = %+v, %v; want COMPLETED", run, err)
	}
	// Same usage prices, a dearer fixed fee: only a fee that was billed can be corrected.
	job, _ := e.rerate("2026-10", e.newVersion(1200, "12.5", "2"))
	if job.Corrections != 0 || job.Credits != 0 || job.CorrectionMinor != 0 {
		t.Fatalf("job = %+v; want no correction for a waived fixed fee", job)
	}
	if k := e.invoiceKinds("2026-10"); k["CORRECTION"] != 0 {
		t.Fatalf("invoices = %v, want no correction", k)
	}
}
