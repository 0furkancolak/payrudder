//go:build integration

package itest

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
	"github.com/0furkancolak/payrudder/services/billing-svc/migrations"
)

// Cells mode of the suite (ADR 0018): ITEST_CELLS=1 go test -tags integration ./internal/itest runs every scenario with every tenant in a schema of a cell database
// instead of the pooled one. The scenarios are the same; only where the rows are, and so how the tests read them, differs.

// cellsMode reports whether the suite runs with every tenant in a cell.
func cellsMode() bool { return os.Getenv("ITEST_CELLS") != "" }

// cellSweep is how often the outbox relay visits the cells in a test: the relay is told by NOTIFY, and this is the net under it.
func cellSweep() time.Duration {
	if cellsMode() {
		return 100 * time.Millisecond
	}
	return 0
}

// tenantSchemas is what a tenant's schema in a cell is built from.
func tenantSchemas() pgxx.TenantSchemas {
	return pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"billing_app", "billing_system"}}
}

// sqlRunner is what a test runs statements through: *pgxpool.Pool in pooled mode, cellSQL in cells mode.
type sqlRunner interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// lazyCells is the directory of the cells-mode suite: a tenant is placed in cell c1 (and its schema provisioned) the first time anything asks where it is, so the
// scenarios, which make up their tenants as they go, need no change. (The jobs' own test, cells_jobs_integration_test.go, moves tenants about with a testkit.Directory.)
type lazyCells struct {
	mu       sync.Mutex
	migrator string
	placed   map[uuid.UUID]bool
}

// place provisions the tenant's schema in the cell, once.
func (l *lazyCells) place(ctx context.Context, id uuid.UUID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.placeLocked(ctx, id)
}

func (l *lazyCells) placeLocked(ctx context.Context, id uuid.UUID) error {
	if l.placed[id] {
		return nil
	}
	if _, err := pgxx.ProvisionTenant(ctx, l.migrator, tenantSchemas(), id, nil); err != nil {
		return err
	}
	l.placed[id] = true
	return nil
}

func (l *lazyCells) isPlaced(id uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.placed[id]
}

func (l *lazyCells) Placement(ctx context.Context, id uuid.UUID) (tenantdir.Placement, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.placeLocked(ctx, id); err != nil {
		return tenantdir.Placement{}, err
	}
	return tenantdir.Placement{TenantID: id, Cell: "c1", Status: tenantdir.StatusReady}, nil
}

func (l *lazyCells) Invalidate(uuid.UUID) {}

func (l *lazyCells) Placed(context.Context, string) ([]tenantdir.Placement, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []tenantdir.Placement
	for id := range l.placed {
		out = append(out, tenantdir.Placement{TenantID: id, Cell: "c1", Status: tenantdir.StatusReady})
	}
	return out, nil
}

// setupCells makes the shared state cells-mode: the cell database, the directory and the routers.
func setupCells(t *testing.T) {
	t.Helper()
	cell := testkit.CreateCellDB(t, shared.pg, "billing", "c1")
	cellApp, err := pgxx.NewPool(context.Background(), cell.App)
	if err != nil {
		t.Fatal(err)
	}
	if shared.cellSys, err = pgxx.NewPool(context.Background(), cell.System); err != nil {
		t.Fatal(err)
	}
	shared.dir = &lazyCells{migrator: cell.Migrator, placed: map[uuid.UUID]bool{}}
	appRouter, err := pgxx.NewRouter(shared.appPool, map[string]*pgxpool.Pool{"c1": cellApp}, shared.dir)
	if err != nil {
		t.Fatal(err)
	}
	if shared.router, err = pgxx.NewRouter(shared.systemPool, map[string]*pgxpool.Pool{"c1": shared.cellSys}, shared.dir); err != nil {
		t.Fatal(err)
	}
	shared.app = appRouter
	shared.system = tenantdb.New(shared.systemPool, shared.router)
	shared.sql = cellSQL{}
}

// deleteAllTenantEvents empties the tenant-event outbox of every tenant of the suite, wherever it is.
func deleteAllTenantEvents(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := shared.systemPool.Exec(ctx, `DELETE FROM tenant_event_outbox`); err != nil {
		t.Fatal(err)
	}
	if shared.router == nil {
		return
	}
	err := shared.router.EachReady(ctx, func(ctx context.Context, p pgxx.PlacedTenant) error {
		return pgxx.InTenantSchemaTx(ctx, p.Pool, p.TenantID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM tenant_event_outbox`)
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// placeTenant puts a tenant of a test in the cell now (a no-op in pooled mode), for the tenants a test writes rows of before any service call asks where they are.
func placeTenant(t *testing.T, id uuid.UUID) {
	t.Helper()
	if shared.dir == nil {
		return
	}
	if err := shared.dir.place(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}

// traceUnplaced prints a statement that names no placed tenant, so that one that should have is found (CELLSQL_TRACE=1): it runs on the pooled database, where a
// cells-mode tenant has no rows.
func traceUnplaced(sql string) {
	if os.Getenv("CELLSQL_TRACE") != "" {
		fmt.Fprintln(os.Stderr, "cellSQL: no tenant named, pooled:", strings.Join(strings.Fields(sql), " "))
	}
}

// cellSQL runs a test's statement in the schema of the one placed tenant whose id is among its arguments, and on the pooled database when there is none.
type cellSQL struct{}

// seedsATenant matches the INSERT of a test's fixture row, whose first column is tenant_id and so whose first argument is the tenant: the tenant is placed in the cell
// by the row that is its first.
var seedsATenant = regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+\w+\s*\(\s*tenant_id\b`)

func uuidOf(a any) uuid.UUID {
	switch v := a.(type) {
	case uuid.UUID:
		return v
	case *uuid.UUID:
		if v != nil {
			return *v
		}
	case string:
		id, _ := uuid.Parse(v)
		return id
	}
	return uuid.Nil
}

func tenantOf(ctx context.Context, sql string, args []any) (uuid.UUID, error) {
	var found uuid.UUID
	for _, a := range args {
		id := uuidOf(a)
		if id == uuid.Nil || !shared.dir.isPlaced(id) {
			continue
		}
		if found != uuid.Nil && found != id {
			return uuid.Nil, fmt.Errorf("cellSQL: the statement names two tenants (%s, %s)", found, id)
		}
		found = id
	}
	if found == uuid.Nil && len(args) > 0 && seedsATenant.MatchString(sql) {
		if id := uuidOf(args[0]); id != uuid.Nil {
			return id, shared.dir.place(ctx, id)
		}
	}
	return found, nil
}

func (cellSQL) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	id, err := tenantOf(ctx, sql, args)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if id == uuid.Nil {
		traceUnplaced(sql)
		return shared.systemPool.Exec(ctx, sql, args...)
	}
	var tag pgconn.CommandTag
	err = pgxx.InTenantSchemaTx(ctx, shared.cellSys, id, func(tx pgx.Tx) (err error) {
		tag, err = tx.Exec(ctx, sql, args...)
		return err
	})
	return tag, err
}

func (cellSQL) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	id, err := tenantOf(ctx, sql, args)
	if err != nil {
		return errRow{err}
	}
	if id == uuid.Nil {
		traceUnplaced(sql)
		return shared.systemPool.QueryRow(ctx, sql, args...)
	}
	return cellRow{ctx: ctx, id: id, sql: sql, args: args}
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// cellRow runs when it is scanned, in a transaction that commits (a statement with RETURNING writes).
type cellRow struct {
	ctx  context.Context
	id   uuid.UUID
	sql  string
	args []any
}

func (r cellRow) Scan(dest ...any) error {
	return pgxx.InTenantSchemaTx(r.ctx, shared.cellSys, r.id, func(tx pgx.Tx) error {
		return tx.QueryRow(r.ctx, r.sql, r.args...).Scan(dest...)
	})
}

func (cellSQL) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	id, err := tenantOf(ctx, sql, args)
	if err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		traceUnplaced(sql)
		return shared.systemPool.Query(ctx, sql, args...)
	}
	tx, err := pgxx.BeginTenantSchema(ctx, shared.cellSys, id)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return &cellRows{Rows: rows, tx: tx, ctx: ctx}, nil
}

// cellRows ends its transaction when it is closed.
type cellRows struct {
	pgx.Rows
	tx   pgx.Tx
	ctx  context.Context
	once sync.Once
}

func (r *cellRows) Close() {
	r.Rows.Close()
	r.once.Do(func() { _ = r.tx.Commit(r.ctx) })
}
