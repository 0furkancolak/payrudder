//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// ITEST_CELLS=1 really puts the suite's tenants in a cell: a store's statement for a fresh tenant lands in its schema and not in the pooled database, and without
// it the rows are in the pooled database.
func TestSuiteModeFollowsTheEnvironment(t *testing.T) {
	pg := testkit.StartPostgres(t)
	w := openDB(t, pg)
	tenant := uuid.New()
	ctx := context.Background()
	if err := (store.Objects{DB: w.DB}).Put(ctx, tenant, "paddle", "live", "price", "k", "ref"); err != nil {
		t.Fatal(err)
	}
	var pooled int
	if err := w.Pooled.QueryRow(ctx, `SELECT count(*) FROM provider_objects WHERE tenant_id = $1`, tenant).Scan(&pooled); err != nil {
		t.Fatal(err)
	}
	var here int
	if err := w.At(tenant).QueryRow(ctx, `SELECT count(*) FROM provider_objects WHERE tenant_id = $1`, tenant).Scan(&here); err != nil || here != 1 {
		t.Fatalf("rows where the tenant lives: %d, %v", here, err)
	}
	if (w.Router != nil) != cellsMode() || (pooled == 1) == cellsMode() {
		t.Fatalf("cells mode %v, but the router is %v and the pooled database holds %d rows", cellsMode(), w.Router != nil, pooled)
	}
}
