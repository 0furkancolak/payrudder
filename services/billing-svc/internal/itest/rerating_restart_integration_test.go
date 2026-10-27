//go:build integration

package itest

import (
	"context"
	"testing"

	reratemodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/model"
)

// A job that failed for good (a plan version that was broken for a while) can be started again once the cause is fixed: the same request runs it, instead of
// returning the dead job for ever. What it writes is keyed by the job, so a restart creates nothing twice.
func testRerateRestartsAFailedJob(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	completeOctober(e)
	higher := e.newVersion(1200, "15", "2")

	first, _ := e.rerate("2026-10", higher)
	if first.Status != reratemodel.Completed || first.Corrections != 2 {
		t.Fatalf("first job = %+v", first)
	}
	if _, err := shared.system.Exec(context.Background(), `UPDATE rerating_jobs SET status = 'FAILED', last_error = 'plan version was unavailable', completed_at = NULL WHERE tenant_id = $1 AND id = $2`, e.tid, first.ID); err != nil {
		t.Fatal(err)
	}
	again, created := e.rerate("2026-10", higher)
	if created || again.ID != first.ID || again.Status != reratemodel.Completed || again.LastError != "" {
		t.Fatalf("restarted job = %+v created=%v; want the same job, COMPLETED, without an error", again, created)
	}
	if again.Corrections != 2 || e.invoiceKinds("2026-10")["CORRECTION"] != 2 {
		t.Fatalf("restart corrections = %d, invoices %v; want 2, not doubled", again.Corrections, e.invoiceKinds("2026-10"))
	}
}
