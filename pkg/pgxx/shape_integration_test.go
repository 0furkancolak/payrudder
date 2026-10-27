//go:build integration

package pgxx_test

import (
	"testing"
	"testing/fstest"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

func TestSchemaShapeSeesDrift(t *testing.T) {
	ctx := testkit.Ctx(t)
	e := newCellEnv(t)
	id := uuid.New()
	if _, err := pgxx.ProvisionTenant(ctx, e.cell.Migrator, widgetSchemas(), id, nil); err != nil {
		t.Fatal(err)
	}
	pooled, err := pgxx.SchemaShape(ctx, e.pooledApp, "public")
	if err != nil {
		t.Fatal(err)
	}
	tenantShape, err := pgxx.SchemaShape(ctx, e.cellMigrator, tenantdir.Schema(id))
	if err != nil {
		t.Fatal(err)
	}
	// The tenant set has a second migration the public one lacks: exactly that column differs.
	diff := pgxx.Diff(pooled, tenantShape)
	if len(diff) != 1 || diff[0] != "+ column widgets.label text notnull=false default= identity= generated=" {
		t.Fatalf("diff = %q, want only the label column", diff)
	}
	// Bring public level and the shapes agree, privileges, policies and functions included.
	both := fstest.MapFS{"00001_widgets.sql": pooledWidgets["00001_widgets.sql"], "00002_label.sql": tenantWidgets["00002_label.sql"]}
	if err := pgxx.Migrate(ctx, e.pooled.Migrator, both, nil); err != nil {
		t.Fatal(err)
	}
	pooled, err = pgxx.SchemaShape(ctx, e.pooledApp, "public")
	if err != nil {
		t.Fatal(err)
	}
	if diff := pgxx.Diff(pooled, tenantShape); len(diff) != 0 {
		t.Fatalf("diff = %q, want none", diff)
	}
	if len(pooled) < 8 {
		t.Fatalf("shape has only %d lines: %q", len(pooled), pooled)
	}
}
