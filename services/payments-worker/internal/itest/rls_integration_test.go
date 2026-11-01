//go:build integration

package itest

import (
	"testing"

	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// payments-worker checks at startup that every table with a tenant_id is under forced row-level security or listed as exempt (docs/plans/22 G11). payments_db has no RLS
// on purpose, so the list is every such table: this fails when a new one is added without being listed, and when a listed one gains RLS and stays on the list.
func TestRowLevelSecurityExemptionsAreExactlyTheTablesWithoutIt(t *testing.T) {
	w := openDB(t, testkit.StartPostgres(t))
	testkit.ForcedRLS(t, w.Pooled, migrations.RLSExempt)
}
