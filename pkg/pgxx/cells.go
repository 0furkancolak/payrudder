package pgxx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// Cells mode (ADR 0018). A tenant placed in a cell has its tables in the schema t_<id> of that cell's database; a transaction for it runs with
//
//	SET LOCAL search_path TO "t_<id>", shared
//
// (transaction-local, so it is safe behind a transaction-mode pooler) and, as in pooled mode, with app.tenant_id set: the schema is where the tenant's data is, and
// row-level security stays on in the tenant schema as the second guard, so a statement that names another tenant's schema still sees none of its rows.
// Queries are the same in both modes; the one function that decides is Router.BeginTenantTx.

// SharedSchema holds what every tenant of a cell shares; it is the second element of a tenant's search_path.
const SharedSchema = "shared"

// TenantBeginner is a Beginner that knows where tenants live. InTenantTx uses BeginTenantTx when its db is one, and a plain Beginner otherwise (pooled mode).
type TenantBeginner interface {
	Beginner
	// BeginTenantTx starts a transaction in the tenant's place, already scoped to the tenant.
	BeginTenantTx(ctx context.Context, tenantID uuid.UUID, opts pgx.TxOptions) (pgx.Tx, error)
	// Invalidate drops what is known about where the tenant lives.
	Invalidate(tenantID uuid.UUID)
}

// Target is where a tenant's data is: a pool, and in a cell the schema.
type Target struct {
	Cell string // "" for the pooled database
	Pool *pgxpool.Pool
	// Schema is the tenant's schema in the cell; "" in the pooled database.
	Schema string
}

// ErrUnknownCell is a placement in a cell this process has no connection to.
var ErrUnknownCell = errors.New("pgxx: cell is not configured in this process")

// Router sends a tenant's transactions to where the directory says its data is: the service's pool of one role in the pooled database, or the pool of
// that role in the tenant's cell. A process builds one Router per role (app, system). With no directory every tenant is pooled, which is what a
// deployment without cells runs.
type Router struct {
	pooled *pgxpool.Pool
	cells  map[string]*pgxpool.Pool
	dir    tenantdir.Source
}

// NewRouter returns a Router over the pooled pool and the pools of the cells (by name). dir may be nil only when there are no cells.
func NewRouter(pooled *pgxpool.Pool, cells map[string]*pgxpool.Pool, dir tenantdir.Source) (*Router, error) {
	if pooled == nil {
		return nil, errors.New("pgxx: router without a pooled pool")
	}
	if len(cells) > 0 && dir == nil {
		return nil, errors.New("pgxx: router with cells needs a directory")
	}
	return &Router{pooled: pooled, cells: cells, dir: dir}, nil
}

// Pooled is the pool of the pooled database.
func (r *Router) Pooled() *pgxpool.Pool { return r.pooled }

// CellNames are the cells this router has a pool for, sorted.
func (r *Router) CellNames() []string {
	names := make([]string, 0, len(r.cells))
	for n := range r.cells {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Cell is the pool of a cell.
func (r *Router) Cell(name string) (*pgxpool.Pool, error) {
	p, ok := r.cells[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCell, name)
	}
	return p, nil
}

// BeginTx implements Beginner on the pooled database, for code that is not about one tenant (a ping, the pooled part of a job). A tenant's work uses
// InTenantTx, which finds the place.
func (r *Router) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	return r.pooled.BeginTx(ctx, opts)
}

// Invalidate implements TenantBeginner.
func (r *Router) Invalidate(tenantID uuid.UUID) {
	if r.dir != nil {
		r.dir.Invalidate(tenantID)
	}
}

// Locate says where the tenant's data is. A tenant that is not serving (moving) is an error that matches tenantdir.ErrNotServing, and so is a
// directory that cannot answer: the router never guesses a place, because a write to the wrong one is not undone by a retry.
func (r *Router) Locate(ctx context.Context, tenantID uuid.UUID) (Target, error) {
	if r.dir == nil {
		return Target{Pool: r.pooled}, nil
	}
	p, err := r.dir.Placement(ctx, tenantID)
	if err != nil {
		return Target{}, err
	}
	if !p.Serving() {
		return Target{}, tenantdir.NotServing(p)
	}
	if p.Pooled() {
		return Target{Pool: r.pooled}, nil
	}
	pool, err := r.Cell(p.Cell)
	if err != nil {
		return Target{}, err
	}
	return Target{Cell: p.Cell, Pool: pool, Schema: tenantdir.Schema(tenantID)}, nil
}

// PlacedTenant is a tenant placed in a cell with the pool of that cell (of the router's role).
type PlacedTenant struct {
	tenantdir.Placement
	Pool *pgxpool.Pool
}

// Placed lists every tenant placed in a cell, whatever its status, asking the directory (not its cache): a job that visits every tenant runs on a timer, and
// must not miss a tenant a cached answer does not know yet. A router with no cells has none. A tenant in a cell this process has no pool for is an error.
func (r *Router) Placed(ctx context.Context) ([]PlacedTenant, error) {
	if len(r.cells) == 0 {
		return nil, nil
	}
	l, ok := r.dir.(tenantdir.Lister)
	if !ok {
		return nil, errors.New("pgxx: the directory of this router cannot list placements")
	}
	ps, err := l.Placed(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]PlacedTenant, 0, len(ps))
	var errs []error
	for _, p := range ps {
		pool, err := r.Cell(p.Cell)
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", p.TenantID, err))
			continue
		}
		out = append(out, PlacedTenant{Placement: p, Pool: pool})
	}
	return out, errors.Join(errs...)
}

// EachReady runs fn for every tenant placed in a cell that is serving, one after the other: the converted form of a job that used to scan one shared table.
// A tenant that is moving is skipped (a job must not touch it), one whose fn fails does not stop the others; the errors come back joined.
func (r *Router) EachReady(ctx context.Context, fn func(ctx context.Context, t PlacedTenant) error) error {
	placed, err := r.Placed(ctx)
	errs := []error{err}
	for _, t := range placed {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !t.Serving() {
			continue
		}
		if err := fn(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t.TenantID, err))
		}
	}
	return errors.Join(errs...)
}

// BeginTenantTx implements TenantBeginner.
func (r *Router) BeginTenantTx(ctx context.Context, tenantID uuid.UUID, opts pgx.TxOptions) (pgx.Tx, error) {
	t, err := r.Locate(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	tx, err := t.Pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := scopeTx(ctx, tx, tenantID, t.Schema); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

// scopeTx is the one place that sets what a tenant's transaction is confined to: the RLS setting always, and in a cell the search path too. set_config(…, true)
// is SET LOCAL.
func scopeTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, schema string) error {
	if schema == "" {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return fmt.Errorf("pgxx: set tenant: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true), set_config('search_path', $2, true)`, tenantID.String(), SearchPath(schema)); err != nil {
		return fmt.Errorf("pgxx: set tenant and search path: %w", err)
	}
	return nil
}

// SearchPath is the search_path of a tenant's transactions: its schema, then the shared one.
func SearchPath(schema string) string { return quoteIdent(schema) + ", " + SharedSchema }

// InTenantSchemaTx runs fn in a transaction on pool whose search path is the tenant's schema, without app.tenant_id: it is for the system role of a cell, which
// bypasses row-level security and names the tenant by the schema (a relay claiming that tenant's outbox, a job running its maintenance).
func InTenantSchemaTx(ctx context.Context, pool Beginner, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	if tenantID == uuid.Nil {
		return errors.New("pgxx: InTenantSchemaTx with nil tenant")
	}
	return InTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('search_path', $1, true)`, SearchPath(tenantdir.Schema(tenantID))); err != nil {
			return fmt.Errorf("pgxx: set search path: %w", err)
		}
		return fn(tx)
	})
}

func quoteIdent(s string) string { return pgx.Identifier{s}.Sanitize() }

// isMissingSchema reports the errors a statement gets when the tenant's schema is not where the router looked: undefined table (the search path names a schema
// that is not there, and nothing is found) or invalid schema name.
func isMissingSchema(err error) bool {
	c := ErrCode(err)
	return c == "42P01" || c == "3F000"
}

// CellDSNs are the connection strings of one cell's database for one service: the three roles.
type CellDSNs struct {
	App      string `json:"app"`
	System   string `json:"system"`
	Migrator string `json:"migrator,omitempty"`
}

// cellName is the same pattern the tenants.cell column checks.
var cellName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// CellSettings is the configuration of cells mode of a service. Off by default: with PAYRUDDER_CELLS unset the service is pooled-only, as it always was.
type CellSettings struct {
	// Cells is a JSON object from cell name to the DSNs of the service's database in that cell:
	// {"c1":{"app":"postgres://…","system":"postgres://…","migrator":"postgres://…"}}. The migrator DSN is needed only to migrate or provision.
	Cells string `env:"PAYRUDDER_CELLS"`
}

// Enabled reports whether any cell is configured.
func (s CellSettings) Enabled() bool { return strings.TrimSpace(s.Cells) != "" }

// Parse reads the cells; an unset value is no cells.
func (s CellSettings) Parse() (map[string]CellDSNs, error) {
	if !s.Enabled() {
		return nil, nil
	}
	var out map[string]CellDSNs
	dec := json.NewDecoder(strings.NewReader(s.Cells))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("PAYRUDDER_CELLS is not a JSON object of cells: %w", err)
	}
	var errs []error
	for name, d := range out {
		if !cellName.MatchString(name) {
			errs = append(errs, fmt.Errorf("PAYRUDDER_CELLS: cell name %q must match %s", name, cellName))
		}
		if d.App == "" || d.System == "" {
			errs = append(errs, fmt.Errorf("PAYRUDDER_CELLS: cell %q needs an app and a system DSN", name))
		}
	}
	return out, errors.Join(errs...)
}

// CheckCells is for the service's Validate.
func (s CellSettings) CheckCells() error {
	_, err := s.Parse()
	return err
}

// Routers are the app-role and system-role routers of a process, and what closes the cell pools.
type Routers struct {
	App, System *Router
	cellPools   []*pgxpool.Pool
}

// Close closes the cell pools (the pooled ones belong to the caller).
func (r *Routers) Close() {
	for _, p := range r.cellPools {
		p.Close()
	}
}

// OpenRouters opens the pools of every configured cell and returns the two routers over them and the service's own pooled pools. With no cells configured the
// routers have no directory and send everything to the pooled pools. On failure no pool stays open.
func (s CellSettings) OpenRouters(ctx context.Context, appPool, systemPool *pgxpool.Pool, dir tenantdir.Source) (*Routers, error) {
	dsns, err := s.Parse()
	if err != nil {
		return nil, err
	}
	rs := &Routers{}
	appCells, sysCells := map[string]*pgxpool.Pool{}, map[string]*pgxpool.Pool{}
	for name, d := range dsns {
		app, err := NewPool(ctx, d.App)
		if err != nil {
			rs.Close()
			return nil, fmt.Errorf("cell %s app: %w", name, err)
		}
		rs.cellPools = append(rs.cellPools, app)
		sys, err := NewPool(ctx, d.System)
		if err != nil {
			rs.Close()
			return nil, fmt.Errorf("cell %s system: %w", name, err)
		}
		rs.cellPools = append(rs.cellPools, sys)
		appCells[name], sysCells[name] = app, sys
	}
	if rs.App, err = NewRouter(appPool, appCells, dir); err != nil {
		rs.Close()
		return nil, err
	}
	if rs.System, err = NewRouter(systemPool, sysCells, dir); err != nil {
		rs.Close()
		return nil, err
	}
	return rs, nil
}
