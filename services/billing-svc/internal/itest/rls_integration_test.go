//go:build integration

package itest

import (
	"testing"

	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/migrations"
)

// testRowLevelSecurityIsForcedOnEveryTenantTable is the check billing-svc makes at startup (docs/plans/22 G11), on the migrated schema: every table with a tenant_id is under
// forced row-level security except the ones migrations.RLSExempt names, and that list names nothing else.
func testRowLevelSecurityIsForcedOnEveryTenantTable(t *testing.T) {
	infra(t)
	testkit.ForcedRLS(t, shared.systemPool, migrations.RLSExempt)
}
