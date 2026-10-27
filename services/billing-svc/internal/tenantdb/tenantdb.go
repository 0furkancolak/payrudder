// Package tenantdb is how billing-svc reaches a tenant's rows with its system role (billing_system, BYPASSRLS), wherever the tenant lives (ADR 0018).
//
// In pooled mode (no PAYRUDDER_CELLS) a DB is the system pool and nothing else: InTx is pgxx.InTx on it, Q is sqlc over it, and a job's Each visits it once, exactly as
// the code did before cells. In cells mode a tenant's rows are in the pooled database or in the schema t_<id> of a cell, the directory says which, and the system role
// names the tenant by that place: a transaction for the tenant runs on the pool that holds its rows with the search path set to its schema.
//
// Two shapes of work need it. Work for one tenant (a request, a reply, a saga step) goes through DB: InTx and Q find the tenant's place by the directory. Work that
// scans for what is due (a sweep, the scheduler tick) goes through DB.Each, which hands the job a Scope for the pooled database and then one for every serving tenant
// placed in a cell. A Scope only reaches the rows of its own place: the scan sees them, and a tenant it names must be one that lives there. So a job runs once per
// tenant (a tenant is in exactly one place), never mixes two tenants in a transaction (a cell scope refuses another tenant's id), and never acts on a row the directory
// no longer puts in that place.
package tenantdb

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Errors of a Scope.
var (
	// ErrWrongTenant is a job in the scope of one tenant asking for another tenant's rows: two tenants never share a transaction.
	ErrWrongTenant = errors.New("tenantdb: the scope belongs to another tenant")
	// ErrMisplaced is a row found in the pooled database for a tenant the directory places in a cell (a move whose last step did not finish): the cell holds the live data,
	// so the pooled leftover is not acted on.
	ErrMisplaced = errors.New("tenantdb: the pooled database holds rows of a tenant the directory places in a cell")
)

// Moving reports whether err says the tenant is being moved: the caller skips it and the next pass finds it again.
func Moving(err error) bool { return errors.Is(err, tenantdir.ErrNotServing) }

// Skippable reports whether a job should leave the row's tenant for the next pass: it is moving, or its rows are not where the scan found them.
func Skippable(err error) bool { return Moving(err) || errors.Is(err, ErrMisplaced) }

// DB reaches the rows of any tenant with the system role.
type DB struct {
	pool  *pgxpool.Pool
	cells *pgxx.Router
}

// New returns a DB over the system pool. cells is the system-role router of cells mode, or nil when cells are off.
func New(pool *pgxpool.Pool, cells *pgxx.Router) *DB { return &DB{pool: pool, cells: cells} }

// Pool is the system pool of the pooled database.
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// Cells reports whether cells mode is on.
func (d *DB) Cells() bool { return d.cells != nil }

// placer finds what opens transactions where a tenant's rows are.
type placer func(ctx context.Context, tenant uuid.UUID) (pgxx.Beginner, error)

// schemaBeginner opens transactions on the pool of a cell, scoped to one tenant's schema (and to its id for the row-level security that stays on as the second guard).
type schemaBeginner struct {
	pool   *pgxpool.Pool
	schema string
	tenant uuid.UUID
}

func (b schemaBeginner) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	tx, err := b.pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true), set_config('search_path', $2, true)`, b.tenant.String(), pgxx.SearchPath(b.schema)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("tenantdb: scope the transaction to %s: %w", b.schema, err)
	}
	return tx, nil
}

func (d *DB) place(ctx context.Context, tenant uuid.UUID) (pgxx.Beginner, error) {
	if d.cells == nil {
		return d.pool, nil
	}
	t, err := d.locate(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if t.Schema == "" {
		return t.Pool, nil
	}
	return schemaBeginner{pool: t.Pool, schema: t.Schema, tenant: tenant}, nil
}

// locate is Router.Locate for the system role, which also works on rows of tenants that no longer exist in plans-svc (a deleted tenant's leftovers, a scan finds
// them): a tenant the directory does not know has no cell, so its rows can only be in the pooled database. Any other failure stays an error; a place is never guessed.
func (d *DB) locate(ctx context.Context, tenant uuid.UUID) (pgxx.Target, error) {
	t, err := d.cells.Locate(ctx, tenant)
	if errors.Is(err, tenantdir.ErrNotFound) {
		return pgxx.Target{Pool: d.pool}, nil
	}
	return t, err
}

// InTx runs fn in a transaction where the tenant's rows are. A tenant that is moving, or whose place the directory cannot give, is an error that matches
// tenantdir.ErrNotServing or the directory's own: the DB never guesses a place.
func (d *DB) InTx(ctx context.Context, tenant uuid.UUID, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	if d.cells == nil {
		return pgxx.InTx(ctx, d.pool, opts, fn)
	}
	return inTx(ctx, d.place, d.cells, tenant, opts, fn)
}

// Q is sqlc over the tenant's place. Each statement runs in a transaction of its own (what a statement on a pool is), so use InTx for anything that must be atomic.
func (d *DB) Q(tenant uuid.UUID) *sqlc.Queries {
	if d.cells == nil {
		return sqlc.New(d.pool)
	}
	return sqlc.New(stmts{place: d.place, cells: d.cells, tenant: tenant})
}

// Check says whether the tenant's rows can be reached now: nil, or the reason (it is moving, the directory cannot say where it is). A job that does a lot of work
// elsewhere (calls to plans-svc) before it touches the tenant's rows asks first.
func (d *DB) Check(ctx context.Context, tenant uuid.UUID) error {
	_, err := d.place(ctx, tenant)
	return err
}

// Acquire takes a connection of the pool that holds the tenant's rows, for work that must stay on one session (an advisory lock).
func (d *DB) Acquire(ctx context.Context, tenant uuid.UUID) (*pgxpool.Conn, error) {
	if d.cells == nil {
		return d.pool.Acquire(ctx)
	}
	t, err := d.locate(ctx, tenant)
	if err != nil {
		return nil, err
	}
	return t.Pool.Acquire(ctx)
}

// Scope is one place a job visits: the pooled database (Tenant is uuid.Nil, and every pooled tenant is in it) or the schema of one tenant placed in a cell.
type Scope struct {
	d      *DB
	tenant uuid.UUID
	cell   *pgxpool.Pool
}

// Tenant is the tenant a cell scope belongs to, and uuid.Nil for the pooled database.
func (s Scope) Tenant() uuid.UUID { return s.tenant }

func (s Scope) place(ctx context.Context, tenant uuid.UUID) (pgxx.Beginner, error) {
	if s.cell != nil {
		if tenant != s.tenant {
			return nil, fmt.Errorf("%w: %s asked for %s", ErrWrongTenant, s.tenant, tenant)
		}
		return schemaBeginner{pool: s.cell, schema: tenantdir.Schema(tenant), tenant: tenant}, nil
	}
	if s.d.cells == nil {
		return s.d.pool, nil
	}
	// A pooled row of a tenant that is moving is left alone (the copy would miss what the job wrote, and the retire would delete it), and so is one the directory puts
	// in a cell now.
	t, err := s.d.locate(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if t.Schema != "" {
		return nil, fmt.Errorf("%w: tenant %s is in cell %s", ErrMisplaced, tenant, t.Cell)
	}
	return s.d.pool, nil
}

// Check says whether the job may act on a row of this tenant found in this scope.
func (s Scope) Check(ctx context.Context, tenant uuid.UUID) error {
	_, err := s.place(ctx, tenant)
	return err
}

// Scan is sqlc over the whole scope, for the query that finds the work: in the pooled database it sees every pooled tenant, in a cell scope the one tenant. Act on
// what it finds with InTx and Q, which check the tenant.
func (s Scope) Scan() *sqlc.Queries {
	if s.cell == nil {
		return sqlc.New(s.d.pool)
	}
	return sqlc.New(stmts{place: s.place, cells: s.d.cells, tenant: s.tenant})
}

// InTx runs fn in a transaction in the scope, for a row of the given tenant.
func (s Scope) InTx(ctx context.Context, tenant uuid.UUID, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	if s.cell == nil && s.d.cells == nil {
		return pgxx.InTx(ctx, s.d.pool, opts, fn)
	}
	return inTx(ctx, s.place, s.d.cells, tenant, opts, fn)
}

// Q is sqlc for the given tenant's rows in the scope; each statement is a transaction of its own.
func (s Scope) Q(tenant uuid.UUID) *sqlc.Queries {
	if s.cell == nil && s.d.cells == nil {
		return sqlc.New(s.d.pool)
	}
	return sqlc.New(stmts{place: s.place, cells: s.d.cells, tenant: tenant})
}

// Each runs a job's pass in every place: the pooled database first, then every tenant placed in a cell that is serving, one after the other (a tenant that is moving is
// skipped, and so is one whose placement the directory cannot give; the next pass visits it). A scope whose pass fails does not stop the others; the errors come back
// joined. With cells off it is one call with the pooled scope.
func (d *DB) Each(ctx context.Context, fn func(ctx context.Context, s Scope) error) error {
	errs := []error{fn(ctx, Scope{d: d})}
	if d.cells != nil {
		errs = append(errs, d.cells.EachReady(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
			return fn(ctx, Scope{d: d, tenant: t.TenantID, cell: t.Pool})
		}))
	}
	return errors.Join(errs...)
}

// Find looks for something that is not named by a tenant (the hash of a link token, the address of a mail inbox): probe runs in the pooled database and then in every
// tenant placed in a cell that is serving, until it says it found. A cell tenant's probe sees that tenant's rows only. When nothing is found and some tenant could not be
// searched (it is moving, or its probe failed), the answer is an error, never "not found": the thing may be in the tenant that was skipped.
func (d *DB) Find(ctx context.Context, probe func(ctx context.Context, q *sqlc.Queries) (found bool, err error)) (bool, error) {
	if found, err := probe(ctx, sqlc.New(d.pool)); err != nil || found {
		return found, err
	}
	if d.cells == nil {
		return false, nil
	}
	placed, err := d.cells.PlacedWithin(ctx, 0)
	if err != nil {
		return false, fmt.Errorf("tenantdb: tenants placed in cells: %w", err)
	}
	var errs []error
	moving := 0
	for _, t := range placed {
		if !t.Serving() {
			moving++
			continue
		}
		s := Scope{d: d, tenant: t.TenantID, cell: t.Pool}
		found, err := probe(ctx, s.Scan())
		if found && err == nil {
			return true, nil
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t.TenantID, err))
		}
	}
	if moving > 0 {
		errs = append(errs, fmt.Errorf("%w: %d tenants are being moved", tenantdir.ErrNotServing, moving))
	}
	return false, errors.Join(errs...)
}

func inTx(ctx context.Context, place placer, cells *pgxx.Router, tenant uuid.UUID, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	b, err := place(ctx, tenant)
	if err != nil {
		return err
	}
	err = pgxx.InTx(ctx, b, opts, fn)
	staleAnswer(cells, tenant, err)
	return err
}

// staleAnswer drops the directory's answer for a tenant when the database says its schema is not where the router looked (a move happened since).
func staleAnswer(cells *pgxx.Router, tenant uuid.UUID, err error) {
	if cells == nil || err == nil {
		return
	}
	if c := pgxx.ErrCode(err); c == "42P01" || c == "3F000" {
		cells.Invalidate(tenant)
	}
}

// stmts is a sqlc.DBTX for one tenant's rows in cells mode: every statement is its own transaction where the rows are, which is what a statement on a pool is.
type stmts struct {
	place  placer
	cells  *pgxx.Router
	tenant uuid.UUID
}

func (s stmts) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	var tag pgconn.CommandTag
	err := inTx(ctx, s.place, s.cells, s.tenant, pgx.TxOptions{}, func(tx pgx.Tx) (err error) {
		tag, err = tx.Exec(ctx, sql, args...)
		return err
	})
	return tag, err
}

func (s stmts) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return &lazyRow{s: s, ctx: ctx, sql: sql, args: args}
}

// lazyRow runs its statement when it is scanned: pgx.Row has no way to carry an error before that.
type lazyRow struct {
	s    stmts
	ctx  context.Context //nolint:containedctx // the row is scanned within the call that made it
	sql  string
	args []any
}

func (r *lazyRow) Scan(dest ...any) error {
	return inTx(r.ctx, r.s.place, r.s.cells, r.s.tenant, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return tx.QueryRow(r.ctx, r.sql, r.args...).Scan(dest...)
	})
}

func (s stmts) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	b, err := s.place(ctx, s.tenant)
	if err != nil {
		return nil, err
	}
	tx, err := b.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(ctx)
		staleAnswer(s.cells, s.tenant, err)
		return nil, err
	}
	return &txRows{Rows: rows, tx: tx, ctx: ctx}, nil
}

// txRows ends the transaction its rows were read in: it commits when the rows are exhausted (so a failed commit is the rows' error), and rolls back when they are closed
// early.
type txRows struct {
	pgx.Rows
	tx  pgx.Tx
	ctx context.Context //nolint:containedctx // the rows are read within the call that made them

	once      sync.Once
	commitErr error
}

func (r *txRows) finish() {
	r.once.Do(func() {
		r.Rows.Close()
		if r.Rows.Err() != nil {
			_ = r.tx.Rollback(r.ctx)
			return
		}
		r.commitErr = r.tx.Commit(r.ctx)
	})
}

func (r *txRows) Next() bool {
	if r.Rows.Next() {
		return true
	}
	r.finish()
	return false
}

func (r *txRows) Close() { r.finish() }

func (r *txRows) Err() error {
	if err := r.Rows.Err(); err != nil {
		return err
	}
	return r.commitErr
}
