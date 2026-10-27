//go:build integration

package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/aggregator/migrations"
)

// The tenant migrations build exactly the schema the public migrations build for the tenant's tables. consumer_offsets is the one public table they leave out: the
// offsets of a consumer group are not a tenant's.
func TestTenantSchemaMatchesPublic(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	pub := testkit.MigratedDB(t, pg, "aggregator", migrations.FS)
	cell := testkit.CreateCellDB(t, pg, "aggregator", "c1")
	id := uuid.New()
	ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"aggregator_app", "aggregator_system"}}
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
	want, err := pgxx.SchemaShape(ctx, pubConn, "public", "consumer_offsets")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgxx.SchemaShape(ctx, cellConn, tenantdir.Schema(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) < 60 {
		t.Fatalf("the public shape has only %d lines: the comparison is not looking at the schema", len(want))
	}
	if diff := pgxx.Diff(want, got); len(diff) > 0 {
		for _, l := range diff {
			t.Error(l)
		}
		t.Fatal("the tenant migrations and the public migrations of the aggregator have drifted apart (- public only, + tenant only)")
	}
	// A second run changes nothing, and the offsets table is not in the tenant schema.
	if n, err := pgxx.ProvisionTenant(ctx, cell.Migrator, ts, id, nil); err != nil || n != 0 {
		t.Fatalf("second provision = %d, %v", n, err)
	}
	var reg *string
	if err := cellConn.QueryRow(ctx, `SELECT to_regclass($1)::text`, tenantdir.Schema(id)+".consumer_offsets").Scan(&reg); err != nil || reg != nil {
		t.Fatalf("consumer_offsets in the tenant schema: %v, %v", reg, err)
	}
}
