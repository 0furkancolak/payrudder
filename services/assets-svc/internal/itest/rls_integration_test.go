//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/assets-svc/migrations"
)

// assets-svc checks at startup that every table with a tenant_id is under forced row-level security (docs/plans/22 G11); it exempts none.
func TestEveryTenantTableIsUnderForcedRowLevelSecurity(t *testing.T) {
	db := testkit.MigratedDB(t, testkit.StartPostgres(t), "assets", migrations.FS)
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	testkit.ForcedRLS(t, pool, nil)
}
