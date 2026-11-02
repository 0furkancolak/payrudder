//go:build integration

package itest

import (
	"testing"

	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/migrations"
)

// The Down of the baseline takes the whole schema away and Up builds the very same one again. A later migration brings its own Down, and this test runs it too.
func TestBaselineMigrationsDownUp(t *testing.T) {
	testkit.MigrationCycle(t, "billing", migrations.FS)
}

// The audit log is a hash chain on this service's migrations (plan 22 G4): written in order by the trigger, whole for the verifier, and a changed entry is found.
func TestAuditChain(t *testing.T) {
	testkit.AuditChain(t, "billing", migrations.FS)
}
