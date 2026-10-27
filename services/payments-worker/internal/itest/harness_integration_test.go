//go:build integration

package itest

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// cellsMode runs the whole suite with every tenant in a cell instead of the pooled database: ITEST_CELLS=1 go test -tags integration ./internal/itest.
func cellsMode() bool { return os.Getenv("ITEST_CELLS") != "" }

// tenantSchemas is what a tenant's schema is built from, as the service ships it.
func tenantSchemas() pgxx.TenantSchemas {
	return pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"payments_app", "payments_system"}}
}

// world is the database side of a test: the pooled database, and in cells mode (ADR 0018) the cells and the router over them. Everything under test runs on DB,
// which is the system pool, or the system-role router when cells are on; At gives a test a way to look at (or break) the rows of one tenant wherever they are.
type world struct {
	t *testing.T
	// DB is what the stores run on.
	DB store.DB
	// Pooled is the system pool of the pooled database, PooledMigrator the DSN of its owner.
	Pooled         *pgxpool.Pool
	PooledMigrator string
	// Router is the system-role router; nil when cells are off.
	Router *pgxx.Router
	Dir    *lazyCells
	cells  map[string]testkit.ServiceDB
	pools  map[string]*pgxpool.Pool
}

// openDB migrates the pooled payments database and, in cells mode, opens a cell c1 that places every tenant on first use, so the scenarios, which make up their
// tenants as they go, need no change.
func openDB(t *testing.T, pg testkit.Postgres) *world {
	t.Helper()
	return openWorld(t, pg, cellsMode(), "c1")
}

// openWorld is openDB with the mode and the cells chosen by the caller: with cells on, each cell is a database of its own on the same Postgres, and a tenant not
// assigned elsewhere is placed in the first.
func openWorld(t *testing.T, pg testkit.Postgres, cells bool, names ...string) *world {
	t.Helper()
	ctx := context.Background()
	db := testkit.MigratedDB(t, pg, "payments", migrations.FS)
	pooled, err := pgxx.NewPool(ctx, db.System)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pooled.Close)
	w := &world{t: t, DB: pooled, Pooled: pooled, PooledMigrator: db.Migrator, cells: map[string]testkit.ServiceDB{}, pools: map[string]*pgxpool.Pool{}}
	if !cells {
		return w
	}
	migrators := map[string]string{}
	routed := map[string]*pgxpool.Pool{}
	for _, name := range names {
		cell := testkit.CreateCellDB(t, pg, "payments", name)
		if err := pgxx.EnsureShared(ctx, cell.Migrator, tenantSchemas().Roles); err != nil {
			t.Fatal(err)
		}
		sys, err := pgxx.NewPool(ctx, cell.System)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(sys.Close)
		w.cells[name], w.pools[name], routed[name], migrators[name] = cell, sys, sys, cell.Migrator
	}
	w.Dir = &lazyCells{migrator: migrators, def: names[0], cellOf: map[uuid.UUID]string{}, provisioned: map[uuid.UUID]bool{}, moving: map[uuid.UUID]bool{}, unknown: map[uuid.UUID]bool{}}
	if w.Router, err = pgxx.NewRouter(pooled, routed, w.Dir); err != nil {
		t.Fatal(err)
	}
	w.DB = w.Router
	return w
}

// lazyCells is the directory of the cells-mode suite: a tenant is placed in a cell (the default one unless the test chose) and its schema built the first time
// anything asks where it is.
type lazyCells struct {
	mu          sync.Mutex
	migrator    map[string]string
	def         string
	cellOf      map[uuid.UUID]string
	provisioned map[uuid.UUID]bool
	moving      map[uuid.UUID]bool
	// unknown tenants are ones the directory has never heard of.
	unknown map[uuid.UUID]bool
}

// assign places a tenant in a cell ("" is the pooled database) before anything has asked.
func (l *lazyCells) assign(id uuid.UUID, cell string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cellOf[id] = cell
}

// forget makes the directory not know a tenant.
func (l *lazyCells) forget(id uuid.UUID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unknown[id] = true
}

// setMoving marks a tenant as being moved (or not).
func (l *lazyCells) setMoving(id uuid.UUID, moving bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.moving[id] = moving
}

func (l *lazyCells) status(id uuid.UUID) tenantdir.Status {
	if l.moving[id] {
		return tenantdir.StatusMoving
	}
	return tenantdir.StatusReady
}

func (l *lazyCells) cell(id uuid.UUID) string {
	if c, ok := l.cellOf[id]; ok {
		return c
	}
	return l.def
}

func (l *lazyCells) Placement(ctx context.Context, id uuid.UUID) (tenantdir.Placement, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unknown[id] {
		return tenantdir.Placement{}, tenantdir.ErrNotFound
	}
	cell := l.cell(id)
	if cell != "" && !l.provisioned[id] {
		if _, err := pgxx.ProvisionTenant(ctx, l.migrator[cell], tenantSchemas(), id, nil); err != nil {
			return tenantdir.Placement{}, err
		}
		l.provisioned[id] = true
	}
	return tenantdir.Placement{TenantID: id, Cell: cell, Status: l.status(id)}, nil
}

func (l *lazyCells) Invalidate(uuid.UUID) {}

func (l *lazyCells) Placed(context.Context, string) ([]tenantdir.Placement, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []tenantdir.Placement
	for id := range l.provisioned {
		if cell := l.cell(id); cell != "" {
			out = append(out, tenantdir.Placement{TenantID: id, Cell: cell, Status: l.status(id)})
		}
	}
	return out, nil
}

// At returns a way to run SQL against the rows of one tenant, wherever they are: the pooled database as it is, or the tenant's schema in its cell.
func (w *world) At(tenant uuid.UUID) *querier { return &querier{w: w, tenant: tenant} }

// CountAll runs a query that returns one number against the pooled database and every tenant schema of every cell, and adds them up: for a question about all
// tenants ("does any row anywhere hold this?").
func (w *world) CountAll(ctx context.Context, sql string, args ...any) (int, error) {
	total := 0
	var n int
	if err := w.Pooled.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return 0, err
	}
	total += n
	for name, pool := range w.pools {
		ids, err := pgxx.TenantSchemaIDs(ctx, pool)
		if err != nil {
			return 0, err
		}
		for _, id := range ids {
			if err := pgxx.InTenantSchemaTx(ctx, pool, id, func(tx pgx.Tx) error { return tx.QueryRow(ctx, sql, args...).Scan(&n) }); err != nil {
				return 0, fmt.Errorf("cell %s tenant %s: %w", name, id, err)
			}
			total += n
		}
	}
	return total, nil
}

// Dirs of a tenant's data, for tests that look at the schema itself.
func (w *world) schemaOf(ctx context.Context, tenant uuid.UUID) (cell string, pool *pgxpool.Pool, err error) {
	if w.Dir == nil {
		return "", w.Pooled, nil
	}
	p, err := w.Dir.Placement(ctx, tenant)
	if err != nil {
		return "", nil, err
	}
	if p.Pooled() {
		return "", w.Pooled, nil
	}
	return p.Cell, w.pools[p.Cell], nil
}

// querier runs SQL as the system role in the place of one tenant's data.
type querier struct {
	w      *world
	tenant uuid.UUID
}

// Exec runs a statement in a transaction of the tenant (committed unless it fails).
func (q *querier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	_, pool, err := q.w.schemaOf(ctx, q.tenant)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if pool == q.w.Pooled {
		return pool.Exec(ctx, sql, args...)
	}
	var tag pgconn.CommandTag
	err = pgxx.InTenantSchemaTx(ctx, pool, q.tenant, func(tx pgx.Tx) (err error) {
		tag, err = tx.Exec(ctx, sql, args...)
		return err
	})
	return tag, err
}

// QueryRow returns a row whose Scan runs the query in a transaction of the tenant.
func (q *querier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return rowFunc(func(dest ...any) error {
		_, pool, err := q.w.schemaOf(ctx, q.tenant)
		if err != nil {
			return err
		}
		if pool == q.w.Pooled {
			return pool.QueryRow(ctx, sql, args...).Scan(dest...)
		}
		return pgxx.InTenantSchemaTx(ctx, pool, q.tenant, func(tx pgx.Tx) error { return tx.QueryRow(ctx, sql, args...).Scan(dest...) })
	})
}

type rowFunc func(dest ...any) error

func (f rowFunc) Scan(dest ...any) error { return f(dest...) }

// Query runs the query in a transaction of the tenant that ends when the rows are closed.
func (q *querier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	_, pool, err := q.w.schemaOf(ctx, q.tenant)
	if err != nil {
		return nil, err
	}
	if pool == q.w.Pooled {
		return pool.Query(ctx, sql, args...)
	}
	tx, err := pgxx.BeginTenantSchema(ctx, pool, q.tenant)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return &txRows{Rows: rows, tx: tx, ctx: ctx}, nil
}

type txRows struct {
	pgx.Rows
	tx  pgx.Tx
	ctx context.Context
}

func (r *txRows) Close() {
	r.Rows.Close()
	_ = r.tx.Rollback(r.ctx)
}
