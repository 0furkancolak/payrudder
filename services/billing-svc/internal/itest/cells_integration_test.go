//go:build integration

package itest

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/migrations"
)

// The tenant migrations build exactly the schema the public migrations build.
func TestTenantSchemaMatchesPublic(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	pub := testkit.MigratedDB(t, pg, "billing", migrations.FS)
	cell := testkit.CreateCellDB(t, pg, "billing", "c1")
	id := uuid.New()
	ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"billing_app", "billing_system"}}
	if _, err := pgxx.ProvisionTenant(ctx, cell.Migrator, ts, id, nil); err != nil {
		t.Fatal(err)
	}
	pubConn, err := pgx.Connect(ctx, pub.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pubConn.Close(ctx) }()
	cellConn, err := pgx.Connect(ctx, cell.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cellConn.Close(ctx) }()
	want, err := pgxx.SchemaShape(ctx, pubConn, "public")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgxx.SchemaShape(ctx, cellConn, tenantdir.Schema(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) < 300 {
		t.Fatalf("the public shape has only %d lines: the comparison is not looking at the schema", len(want))
	}
	if diff := pgxx.Diff(want, got); len(diff) > 0 {
		for _, l := range diff {
			t.Error(l)
		}
		t.Fatal("the tenant migrations and the public migrations of billing-svc have drifted apart (- public only, + tenant only)")
	}
}
