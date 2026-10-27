//go:build integration

package itest

import (
	"testing"

	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// The Down of the baseline takes the whole schema away and Up builds the very same one again. A later migration brings its own Down, and this test runs it too.
func TestBaselineMigrationsDownUp(t *testing.T) {
	testkit.MigrationCycle(t, "payments", migrations.FS)
}
