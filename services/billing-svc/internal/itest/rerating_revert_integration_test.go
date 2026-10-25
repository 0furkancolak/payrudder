//go:build integration

package itest

import (
	"testing"

	"github.com/google/uuid"

	reratemodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/model"
)

// A re-rating that was a mistake is undone by running the original version again: the subscriptions are on that version, but what was billed is not what it
// prices, so the corrections are given back as credits. Before, a job for the version the subscription is on skipped every customer.
func testRerateBackToTheOriginalVersionUndoesACorrection(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	completeOctober(e)
	original := uuid.MustParse(e.pv().Id)
	higher := e.newVersion(1200, "15", "2")

	up, _ := e.rerate("2026-10", higher)
	if up.Corrections != 2 || up.CorrectionMinor != 203+200 {
		t.Fatalf("first job = %+v, want 2 corrections summing to 403", up)
	}
	back, created := e.rerate("2026-10", original)
	if !created || back.Status != reratemodel.Completed || back.Corrections != 0 || back.Credits != 2 || back.CreditMinor != 203+200 {
		t.Fatalf("job back to the original = %+v created=%v, want 2 credits summing to 403 and no correction", back, created)
	}
	if got := e.credits(); got["cus_a"] != 203 || got["cus_b"] != 200 {
		t.Fatalf("credits = %v, want cus_a 203 and cus_b 200", got)
	}
	// Running it again changes nothing: the period is back at what the original version prices.
	if again, _ := e.rerate("2026-10", original); again.ID != back.ID || again.Credits != 2 {
		t.Fatalf("a repeat = %+v, want the same job", again)
	}
}
