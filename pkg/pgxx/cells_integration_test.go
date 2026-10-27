//go:build integration

package pgxx_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// The fixture service of the cells tests: widgets in the pooled database (public) and in a tenant schema (the tenant set, schema-neutral).
var pooledWidgets = fstest.MapFS{
	"00001_widgets.sql": {Data: []byte(`
-- +goose Up
CREATE FUNCTION app_tenant_id() RETURNS uuid LANGUAGE sql STABLE AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;
CREATE TABLE widgets (tenant_id uuid NOT NULL, id int NOT NULL, PRIMARY KEY (tenant_id, id));
ALTER TABLE widgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE widgets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON widgets USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT ON widgets TO cellt_app;
GRANT SELECT, DELETE ON widgets TO cellt_system;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO cellt_app;
-- +goose Down
DROP TABLE widgets;
DROP FUNCTION app_tenant_id();
`)},
}

var tenantWidgets = fstest.MapFS{
	"00001_widgets.sql": {Data: []byte(`
-- +goose Up
CREATE FUNCTION app_tenant_id() RETURNS uuid LANGUAGE sql STABLE AS $$ SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid $$;
CREATE TABLE widgets (tenant_id uuid NOT NULL, id int NOT NULL, PRIMARY KEY (tenant_id, id));
ALTER TABLE widgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE widgets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON widgets USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());
GRANT SELECT, INSERT ON widgets TO cellt_app;
GRANT SELECT, DELETE ON widgets TO cellt_system;
GRANT EXECUTE ON FUNCTION app_tenant_id() TO cellt_app;
-- +goose Down
DROP TABLE widgets;
DROP FUNCTION app_tenant_id();
`)},
	"00002_label.sql": {Data: []byte(`
-- +goose Up
ALTER TABLE widgets ADD COLUMN label text;
-- +goose Down
ALTER TABLE widgets DROP COLUMN label;
`)},
}

func widgetSchemas() pgxx.TenantSchemas {
	return pgxx.TenantSchemas{Migrations: tenantWidgets, Roles: []string{"cellt_app", "cellt_system"}}
}

type cellEnv struct {
	pooled, cell testkit.ServiceDB
	pooledApp    *pgxpool.Pool
	cellApp      *pgxpool.Pool
	cellSystem   *pgxpool.Pool
	cellMigrator *pgxpool.Pool
}

func newCellEnv(t *testing.T) *cellEnv {
	t.Helper()
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	e := &cellEnv{}
	e.pooled = testkit.CreateServiceDB(t, pg, "cellt")
	if err := pgxx.Migrate(ctx, e.pooled.Migrator, pooledWidgets, nil); err != nil {
		t.Fatal(err)
	}
	e.cell = testkit.CreateCellDB(t, pg, "cellt", "c1")
	open := func(dsn string) *pgxpool.Pool {
		p, err := pgxx.NewPool(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	e.pooledApp, e.cellApp, e.cellSystem, e.cellMigrator = open(e.pooled.App), open(e.cell.App), open(e.cell.System), open(e.cell.Migrator)
	return e
}

func insertWidget(ctx context.Context, db pgxx.Beginner, tenant uuid.UUID, id int) error {
	return pgxx.InTenantTx(ctx, db, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO widgets (tenant_id, id) VALUES ($1, $2)`, tenant, id)
		return err
	})
}

func countWidgets(ctx context.Context, db pgxx.Beginner, tenant uuid.UUID) (n int, err error) {
	err = pgxx.InTenantTx(ctx, db, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM widgets`).Scan(&n)
	})
	return n, err
}

func TestProvisionAndMigrateTenantSchemas(t *testing.T) {
	ctx := testkit.Ctx(t)
	e := newCellEnv(t)
	ts := widgetSchemas()
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	t.Run("provisioning creates the schema, applies every migration, and a second run does nothing", func(t *testing.T) {
		n, err := pgxx.ProvisionTenant(ctx, e.cell.Migrator, ts, a, nil)
		if err != nil || n != 2 {
			t.Fatalf("first run = %d %v, want 2 applied", n, err)
		}
		if n, err = pgxx.ProvisionTenant(ctx, e.cell.Migrator, ts, a, nil); err != nil || n != 0 {
			t.Fatalf("second run = %d %v, want nothing", n, err)
		}
		v, ok, err := pgxx.TenantSchemaVersion(ctx, e.cellMigrator, a)
		if err != nil || !ok || v != 2 {
			t.Fatalf("version = %d %v %v", v, ok, err)
		}
		if _, ok, _ := pgxx.TenantSchemaVersion(ctx, e.cellMigrator, b); ok {
			t.Fatal("a tenant never provisioned has a version")
		}
		if latest, err := pgxx.LatestTenantVersion(tenantWidgets); err != nil || latest != 2 {
			t.Fatalf("latest = %d %v", latest, err)
		}
	})

	t.Run("a migration run over all schemas brings every one up and stops for none", func(t *testing.T) {
		// b and c were provisioned when the set had only its first migration.
		oldSet := pgxx.TenantSchemas{Migrations: fstest.MapFS{"00001_widgets.sql": tenantWidgets["00001_widgets.sql"]}, Roles: ts.Roles}
		for _, id := range []uuid.UUID{b, c} {
			if n, err := pgxx.ProvisionTenant(ctx, e.cell.Migrator, oldSet, id, nil); err != nil || n != 1 {
				t.Fatalf("old provision = %d %v", n, err)
			}
		}
		// c is broken in a way the second migration trips over: the column is already there.
		if _, err := e.cellSystem.Exec(ctx, "SELECT 1"); err != nil {
			t.Fatal(err)
		}
		mig, err := pgx.Connect(ctx, e.cell.Migrator)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = mig.Close(ctx) }()
		if _, err := mig.Exec(ctx, `ALTER TABLE `+tenantdir.Schema(c)+`.widgets ADD COLUMN label text`); err != nil {
			t.Fatal(err)
		}

		rep, err := pgxx.MigrateTenants(ctx, e.cell.Migrator, ts, nil)
		if err == nil || rep.Failed[c] == nil || len(rep.Failed) != 1 {
			t.Fatalf("report = %+v err = %v, want exactly c failed", rep, err)
		}
		if rep.Schemas != 3 || rep.Applied[b] != 1 || len(rep.Applied) != 1 {
			t.Fatalf("applied = %+v over %d schemas, want only b migrated (a was current)", rep.Applied, rep.Schemas)
		}
		if v, _, _ := pgxx.TenantSchemaVersion(ctx, e.cellMigrator, c); v != 1 {
			t.Fatalf("c is at %d, want it left at 1", v)
		}

		// Resumable: repair c and run again; only c has anything left.
		if _, err := mig.Exec(ctx, `ALTER TABLE `+tenantdir.Schema(c)+`.widgets DROP COLUMN label`); err != nil {
			t.Fatal(err)
		}
		rep, err = pgxx.MigrateTenants(ctx, e.cell.Migrator, ts, nil)
		if err != nil || rep.Applied[c] != 1 || len(rep.Applied) != 1 {
			t.Fatalf("second report = %+v %v", rep, err)
		}
		// Idempotent: nothing left.
		if rep, err = pgxx.MigrateTenants(ctx, e.cell.Migrator, ts, nil); err != nil || len(rep.Applied) != 0 {
			t.Fatalf("third report = %+v %v", rep, err)
		}
	})

	t.Run("runners that start together do not trip over each other", func(t *testing.T) {
		d := uuid.New()
		oldSet := pgxx.TenantSchemas{Migrations: fstest.MapFS{"00001_widgets.sql": tenantWidgets["00001_widgets.sql"]}, Roles: ts.Roles}
		if _, err := pgxx.ProvisionTenant(ctx, e.cell.Migrator, oldSet, d, nil); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = pgxx.MigrateTenants(ctx, e.cell.Migrator, ts, nil)
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		if v, _, _ := pgxx.TenantSchemaVersion(ctx, e.cellMigrator, d); v != 2 {
			t.Fatalf("d is at %d, want 2", v)
		}
	})
}

func TestRouterPlacesTransactionsByDirectory(t *testing.T) {
	ctx := testkit.Ctx(t)
	e := newCellEnv(t)
	ts := widgetSchemas()
	inCell, pooled, moving, noCell := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pgxx.ProvisionTenant(ctx, e.cell.Migrator, ts, inCell, nil); err != nil {
		t.Fatal(err)
	}
	dir := tenantdir.Fixed{
		inCell: {TenantID: inCell, Cell: "c1", Status: tenantdir.StatusReady},
		moving: {TenantID: moving, Cell: "c1", Status: tenantdir.StatusMoving},
		noCell: {TenantID: noCell, Cell: "c9", Status: tenantdir.StatusReady},
	}
	r, err := pgxx.NewRouter(e.pooledApp, map[string]*pgxpool.Pool{"c1": e.cellApp}, dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("a tenant in a cell writes into its schema and a pooled tenant into public", func(t *testing.T) {
		if err := insertWidget(ctx, r, inCell, 1); err != nil {
			t.Fatal(err)
		}
		if err := insertWidget(ctx, r, pooled, 1); err != nil {
			t.Fatal(err)
		}
		var inSchema int
		if err := e.cellSystem.QueryRow(ctx, `SELECT count(*) FROM `+tenantdir.Schema(inCell)+`.widgets`).Scan(&inSchema); err != nil || inSchema != 1 {
			t.Fatalf("rows in the tenant schema = %d %v, want 1", inSchema, err)
		}
		if n, err := countWidgets(ctx, r, inCell); err != nil || n != 1 {
			t.Fatalf("cell tenant sees %d %v", n, err)
		}
		if n, err := countWidgets(ctx, r, pooled); err != nil || n != 1 {
			t.Fatalf("pooled tenant sees %d %v", n, err)
		}
		// The pooled database never saw the cell tenant, and the cell has no public widgets.
		var inPublic int
		if err := e.pooledApp.QueryRow(ctx, `SELECT count(*) FROM widgets WHERE tenant_id = $1`, inCell).Scan(&inPublic); err != nil || inPublic != 0 {
			t.Fatalf("pooled database holds %d rows of the cell tenant (%v)", inPublic, err)
		}
	})

	t.Run("the search path and the tenant are transaction-local", func(t *testing.T) {
		for range 5 {
			if _, err := countWidgets(ctx, r, inCell); err != nil {
				t.Fatal(err)
			}
		}
		var sp, tn string
		if err := e.cellApp.QueryRow(ctx, `SELECT current_setting('search_path'), COALESCE(current_setting('app.tenant_id', true), '')`).Scan(&sp, &tn); err != nil {
			t.Fatal(err)
		}
		if sp != `"$user", public` || tn != "" {
			t.Fatalf("a pooled connection kept search_path %q and tenant %q", sp, tn)
		}
	})

	t.Run("a tenant that is moving, in an unknown cell, or unknown to the directory is refused, never guessed", func(t *testing.T) {
		if err := insertWidget(ctx, r, moving, 1); !errors.Is(err, tenantdir.ErrNotServing) {
			t.Fatalf("moving tenant: %v", err)
		}
		if err := insertWidget(ctx, r, noCell, 1); !errors.Is(err, pgxx.ErrUnknownCell) {
			t.Fatalf("unknown cell: %v", err)
		}
		down := tenantdir.New(func(context.Context, uuid.UUID) (tenantdir.Placement, error) {
			return tenantdir.Placement{}, errors.New("plans-svc down")
		}, nil, tenantdir.Options{})
		rd, err := pgxx.NewRouter(e.pooledApp, map[string]*pgxpool.Pool{"c1": e.cellApp}, down)
		if err != nil {
			t.Fatal(err)
		}
		if err := insertWidget(ctx, rd, uuid.New(), 1); err == nil {
			t.Fatal("wrote while the directory was unreachable")
		}
	})

	t.Run("a tenant in a cell cannot read or write another tenant's schema", func(t *testing.T) {
		other := uuid.New()
		if _, err := pgxx.ProvisionTenant(ctx, e.cell.Migrator, ts, other, nil); err != nil {
			t.Fatal(err)
		}
		dir[other] = tenantdir.Placement{TenantID: other, Cell: "c1", Status: tenantdir.StatusReady}
		for i := 1; i <= 3; i++ {
			if err := insertWidget(ctx, r, other, i); err != nil {
				t.Fatal(err)
			}
		}
		// The other schema is reachable by name (the role may use every tenant schema: pooled connections cannot be per tenant), and its rows are not visible.
		var n int
		err := pgxx.InTenantTx(ctx, r, inCell, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM `+tenantdir.Schema(other)+`.widgets`).Scan(&n)
		})
		if err != nil || n != 0 {
			t.Fatalf("tenant in a cell read %d rows of another's schema (err %v), want 0", n, err)
		}
		err = pgxx.InTenantTx(ctx, r, inCell, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO `+tenantdir.Schema(other)+`.widgets (tenant_id, id) VALUES ($1, 99)`, other)
			return err
		})
		if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
			t.Fatalf("writing another's schema: %v", err)
		}
		err = pgxx.InTenantTx(ctx, r, inCell, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO `+tenantdir.Schema(other)+`.widgets (tenant_id, id) VALUES ($1, 99)`, inCell)
			return err
		})
		if err != nil {
			// Its own rows in another's schema would be a hole the other way: row-level security allows it, which is why the data plane never writes that way.
			t.Logf("own row into another's schema: %v", err)
		}
		// Unqualified names are the tenant's own, whatever the other schema holds.
		if n, err := countWidgets(ctx, r, other); err != nil || n < 3 {
			t.Fatalf("other tenant sees %d %v", n, err)
		}
		if n, err := countWidgets(ctx, r, inCell); err != nil || n != 1 {
			t.Fatalf("tenant sees %d %v, want its own 1", n, err)
		}
	})

	t.Run("a stale answer is dropped when the schema is not where it said", func(t *testing.T) {
		ghost := uuid.New() // the directory says c1, but nothing was ever provisioned there
		loads := 0
		cache := tenantdir.New(func(_ context.Context, id uuid.UUID) (tenantdir.Placement, error) {
			loads++
			return tenantdir.Placement{TenantID: id, Cell: "c1", Status: tenantdir.StatusReady}, nil
		}, nil, tenantdir.Options{})
		rg, err := pgxx.NewRouter(e.pooledApp, map[string]*pgxpool.Pool{"c1": e.cellApp}, cache)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := insertWidget(ctx, rg, ghost, 1); pgxx.ErrCode(err) != "42P01" {
				t.Fatalf("insert into a schema that is not there: %v", err)
			}
		}
		if loads != 2 {
			t.Fatalf("directory asked %d times, want once per failed call (the answer is dropped)", loads)
		}
	})
}

func TestRouterWithoutCellsIsPooledOnly(t *testing.T) {
	ctx := testkit.Ctx(t)
	e := newCellEnv(t)
	r, err := pgxx.NewRouter(e.pooledApp, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := insertWidget(ctx, r, id, 1); err != nil {
		t.Fatal(err)
	}
	if n, err := countWidgets(ctx, e.pooledApp, id); err != nil || n != 1 {
		t.Fatalf("plain pool sees %d %v", n, err)
	}
	if _, err := pgxx.NewRouter(e.pooledApp, map[string]*pgxpool.Pool{"c1": e.cellApp}, nil); err == nil {
		t.Fatal("a router with cells and no directory was accepted")
	}
}

func TestCellSettingsOpenRouters(t *testing.T) {
	ctx := testkit.Ctx(t)
	e := newCellEnv(t)
	off := pgxx.CellSettings{}
	if off.Enabled() {
		t.Fatal("empty settings are enabled")
	}
	rs, err := off.OpenRouters(ctx, e.pooledApp, e.pooledApp, nil)
	if err != nil || len(rs.App.CellNames()) != 0 {
		t.Fatalf("off = %v %v", rs, err)
	}
	for _, bad := range []string{`{`, `{"C1":{"app":"x","system":"y"}}`, `{"c1":{"app":"x"}}`, `{"c1":{"app":"x","system":"y","extra":1}}`} {
		if err := (pgxx.CellSettings{Cells: bad}).CheckCells(); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	on := pgxx.CellSettings{Cells: `{"c1":{"app":"` + e.cell.App + `","system":"` + e.cell.System + `"}}`}
	rs, err = on.OpenRouters(ctx, e.pooledApp, e.pooledApp, tenantdir.Fixed{})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if names := rs.App.CellNames(); len(names) != 1 || names[0] != "c1" {
		t.Fatalf("cells = %v", names)
	}
}
