//go:build integration

package itest

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	saascmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/migrations"
)

// The tables of the public schema that are not in a tenant's schema (see migrations/tenant/00001_init.sql): the global tables, and the two with a tenant_id that are
// looked up without knowing the tenant (the api keys by prefix, the queue of the reset runner).
var globalTables = []string{
	"accounts", "tenants", "apps", "users", "account_members", "account_invites", "user_sessions", "user_tokens", "user_identities", "login_codes", "user_onboarding",
	"config_promotions", "app_audit", "go_live_audit", "api_keys", "sandbox_resets",
}

// The functions of the public schema that belong to those tables.
var globalFunctions = []string{
	"api_keys_check_app_not_archived", "api_keys_check_tenant", "config_promotions_one_way", "go_live_audit_live_only", "sandbox_resets_only_sandbox", "tenants_immutable_settings",
}

// The tenant migrations build exactly the tables the public migrations build for the tenant's data: same columns, constraints, indexes, policies, triggers, grants
// and function signatures. The differences are the ones the tenant set documents: no foreign key to users (affiliates.user_id), and current_tenant() reads the
// tenant's row from a transaction setting instead of the tenants table (its signature is the same, so the comparison covers it).
func TestTenantSchemaMatchesPublic(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	pub := testkit.MigratedDB(t, pg, "plans", migrations.FS)
	cell := testkit.CreateCellDB(t, pg, "plans", "c1")
	id := uuid.New()
	if _, err := pgxx.ProvisionTenant(ctx, cell.Migrator, tenantSchemas(), id, nil); err != nil {
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
	want, err := pgxx.SchemaShape(ctx, pubConn, "public", globalTables...)
	if err != nil {
		t.Fatal(err)
	}
	// SchemaShape cannot leave a function out: the ones of the global tables, and the ones btree_gist brings (it is created once per cell database, in `shared`),
	// are taken out of the public side here.
	rows, err := pubConn.Query(ctx, `SELECT p.proname FROM pg_proc p JOIN pg_depend d ON d.objid = p.oid AND d.classid = 'pg_proc'::regclass AND d.deptype = 'e'
		JOIN pg_extension x ON x.oid = d.refobjid WHERE x.extname = 'btree_gist'`)
	if err != nil {
		t.Fatal(err)
	}
	extension, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	want = slices.DeleteFunc(want, func(l string) bool {
		for _, f := range append(slices.Clone(globalFunctions), extension...) {
			if strings.HasPrefix(l, "function "+f+"(") {
				return true
			}
		}
		return false
	})
	got, err := pgxx.SchemaShape(ctx, cellConn, tenantdir.Schema(id))
	if err != nil {
		t.Fatal(err)
	}
	// The trigger functions of a tenant schema bind the schema they live in (SET search_path FROM CURRENT): they are also called by a move, on a connection whose path is
	// not the tenant's, and by the owner's tools. In public the same functions need no binding. A function that runs with its owner's rights (definer) is compared as is.
	for i, l := range got {
		if strings.HasPrefix(l, "function ") && strings.Contains(l, " definer=false ") {
			got[i] = strings.Replace(l, " config=[search_path=<own>]", " config=[]", 1)
		}
	}
	slices.Sort(got)
	if len(want) < 100 {
		t.Fatalf("the public shape has only %d lines: the comparison is not looking at the schema", len(want))
	}
	// The one foreign key the tenant set drops.
	want = slices.DeleteFunc(want, func(l string) bool { return strings.HasPrefix(l, "constraint affiliates affiliates_user_id_fkey ") })
	if diff := pgxx.Diff(want, got); len(diff) > 0 {
		for _, l := range diff {
			t.Error(l)
		}
		t.Fatal("the tenant migrations and the public migrations of plans-svc have drifted apart (- public only, + tenant only)")
	}
	// Every table that has a tenant_id is in the tenant set or on the list of the global ones: a new table has to be placed on purpose.
	rows, err = pubConn.Query(ctx, `SELECT c.table_name FROM information_schema.columns c JOIN information_schema.tables t ON t.table_name = c.table_name AND t.table_schema = c.table_schema
		WHERE c.table_schema = 'public' AND c.column_name = 'tenant_id' AND t.table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	withTenant, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range withTenant {
		if slices.Contains(globalTables, tb) {
			continue
		}
		if !slices.ContainsFunc(got, func(l string) bool { return strings.HasPrefix(l, "table "+tb+" ") }) {
			t.Errorf("table %s has a tenant_id and is neither in the tenant schema nor on the list of global tables", tb)
		}
	}
}

// A tenant of plans-svc moves from the pooled database to a cell and back with every row of its 36 tables (the move is pkg/pgxx's; this is the tenant set under it: foreign
// keys between the tables, the exclusion constraint of subscriptions, the triggers, the identity of the outbox).
//
// The tenant is a sandbox, on purpose: the last step of a move out of the pooled database deletes the tenant's rows there, and the pooled triggers of affiliate_journal and
// affiliate_attributions (append-only, set once) refuse that for a live tenant. They allow it for a sandbox, which is what a reset does. Moving a live tenant that has
// partner journal rows or attributions out of the pooled database is therefore refused at its last step until those two trigger functions also allow the delete of a
// tenant that has a cell (a public migration; see the runbook).
func TestPlansTenantMoves(t *testing.T) {
	ctx := testkit.Ctx(t)
	w := startWorld(t, true)
	e := newEnvIn(t, w, saascmd.Config{})
	sbx := e.account(t, "Mover")[plansv1.Environment_ENVIRONMENT_SANDBOX]
	id := uuid.MustParse(sbx.GetId())
	w.dir.keepPooled(id)
	seed(t, e, sbx.GetId(), "mv")
	want := fingerprint(t, e, sbx.GetId())
	pooled := pgxx.Endpoint{MigratorDSN: w.db.Migrator}
	cell := pgxx.Endpoint{Cell: "c1", MigratorDSN: w.dir.migrator}

	move := func(from, to pgxx.Endpoint) pgxx.CopyReport {
		t.Helper()
		m := pgxx.Move{TenantID: id, From: from, To: to, Schemas: tenantSchemas()}
		if err := m.Fence(ctx); err != nil {
			t.Fatal(err)
		}
		rep, err := m.Copy(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// The directory points at the destination.
		if to.Pooled() {
			w.dir.keepPooled(id)
			if _, err := e.system.Exec(ctx, `UPDATE tenants SET cell = '' WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		} else {
			w.dir.place(id)
			if _, err := e.system.Exec(ctx, `UPDATE tenants SET cell = 'c1' WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := m.RetireSource(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		return rep
	}
	same := func(what string, ignore ...string) {
		t.Helper()
		got := fingerprint(t, e, sbx.GetId())
		for table, w := range want {
			if slices.Contains(ignore, table) {
				continue
			}
			if got[table] != w {
				t.Errorf("%s: table %s differs: want %s, got %s", what, table, w, got[table])
			}
		}
	}

	rep := move(pooled, cell)
	if rep.Rows() < 30 {
		t.Fatalf("only %d rows were copied", rep.Rows())
	}
	same("in the cell")
	if n := (&jobWorld{e: e, w: w}).pooledCount(t, sbx.GetId(), "customers"); n != 0 {
		t.Fatalf("%d customers are left in the pooled database", n)
	}
	// The tenant works in its new place.
	if _, err := e.plans.GetCustomer(as(sbx.GetId()), &plansv1.GetCustomerRequest{ExternalId: "cus_mv"}); err != nil {
		t.Fatalf("a request after the move: %v", err)
	}
	move(cell, pooled)
	// Identity values are renumbered into the pooled database, so the outbox rows have other ids.
	same("back in the pooled database", "outbox")
	if _, err := e.plans.GetCustomer(as(sbx.GetId()), &plansv1.GetCustomerRequest{ExternalId: "cus_mv"}); err != nil {
		t.Fatalf("a request after the move back: %v", err)
	}
}
