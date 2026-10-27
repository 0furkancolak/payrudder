// Package placement is how plans-svc reaches the rows of a tenant when they may be in a cell (phase 28, ADR 0018).
//
// plans-svc keeps its global tables (accounts, tenants, apps, users, members, sessions, tokens, invitations, audits, the keys and the reset queue) in the pooled
// database for ever; only the 36 tables that have a tenant_id and are looked at one tenant at a time move to the tenant's schema in a cell. So a statement is either
// about global rows, which run on the pooled pools as they always did, or about a tenant's rows, which go through here:
//
//   - a request of a tenant (app role) opens its transaction on an AppDB, which is the plain app pool with cells off and a router wrapper with them on;
//   - a job or a call for a person (system role) names the tenant and asks Places where it is: OnTenant runs on the pooled connection it was given when the tenant is
//     pooled (what the code did before), and in a transaction of its own in the tenant's schema when it is in a cell; Each* visits every tenant placed in a cell.
//
// Nothing here guesses a place: a tenant that is moving, or whose placement cannot be had, is refused.
package placement

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// RowSetting is the transaction setting that carries the tenant's own row (a jsonb copy of its line in tenants) into a tenant's schema in a cell, where the table
// is not: current_tenant() of the tenant migrations reads it. It is set by Tenants and by nothing else.
const RowSetting = "app.tenant_row"

// Tenants is a pgxx.TenantBeginner over a Router. It does what the router does and, for a tenant in a cell, also sets RowSetting, because the tenant schema has no
// tenants table to read the tenant's environment, status, tier and zone from. The row is read from the pooled database by the settings pool (the system role).
type Tenants struct {
	router   *pgxx.Router
	settings rowReader
}

// rowReader reads one row of the pooled database as the system role: a pool, or a transaction of a caller that holds a connection to it already.
type rowReader interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewTenants returns the wrapper. router is one role's router; settings is a pool that can read tenants (the pooled system pool).
func NewTenants(router *pgxx.Router, settings *pgxpool.Pool) *Tenants {
	return &Tenants{router: router, settings: settings}
}

// reading is the same wrapper reading the tenant's row from db, a connection the caller already holds. A caller inside a transaction of the pooled system pool must not
// ask the pool for a second connection while it waits for the tenant's transaction: with every connection held by callers in the same position, none would ever be free.
func (t *Tenants) reading(db rowReader) *Tenants { return &Tenants{router: t.router, settings: db} }

// BeginTx implements pgxx.Beginner on the pooled database.
func (t *Tenants) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	return t.router.BeginTx(ctx, opts)
}

// Invalidate implements pgxx.TenantBeginner.
func (t *Tenants) Invalidate(id uuid.UUID) { t.router.Invalidate(id) }

// BeginTenantTx implements pgxx.TenantBeginner.
func (t *Tenants) BeginTenantTx(ctx context.Context, id uuid.UUID, opts pgx.TxOptions) (pgx.Tx, error) {
	target, err := t.router.Locate(ctx, id)
	if err != nil {
		return nil, err
	}
	if target.Schema == "" {
		return t.router.BeginTenantTx(ctx, id, opts)
	}
	row, err := t.row(ctx, id)
	if err != nil {
		return nil, err
	}
	tx, err := t.router.BeginTenantTx(ctx, id, opts)
	if err != nil {
		return nil, err
	}
	if row != "" {
		if _, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, RowSetting, row); err != nil {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("placement: set tenant row: %w", err)
		}
	}
	return tx, nil
}

// row is the tenant's line in tenants as jsonb text; "" for an id that is no tenant (current_tenant() then answers no row, which the callers treat as "not found").
func (t *Tenants) row(ctx context.Context, id uuid.UUID) (string, error) {
	var row string
	err := t.settings.QueryRow(ctx, `SELECT to_jsonb(t)::text FROM tenants t WHERE t.id = $1`, id).Scan(&row)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("placement: read tenant row: %w", err)
	}
	return row, nil
}

// AppDB is what the app-role modules open their tenant transactions on: the app pool itself with cells off (exactly what they used before), and a Tenants over the
// app-role router with them on.
func AppDB(appPool *pgxpool.Pool, router *pgxx.Router, settings *pgxpool.Pool) pgxx.Beginner {
	if router == nil {
		return appPool
	}
	return NewTenants(router, settings)
}

// Places is the system role's view of where tenants are. The zero value is not usable; a nil *Places is "cells are off, the pool is the one you have", which is what
// the modules that are not given one (a unit test) get.
type Places struct {
	pool    *pgxpool.Pool
	router  *pgxx.Router
	tenants *Tenants
}

// NewPlaces returns Places over the pooled system pool and, with cells on, the system-role router. A nil router is cells off.
func NewPlaces(systemPool *pgxpool.Pool, router *pgxx.Router) *Places {
	p := &Places{pool: systemPool, router: router}
	if router != nil {
		p.tenants = NewTenants(router, systemPool)
	}
	return p
}

// Pool is the pooled system pool.
func (p *Places) Pool() *pgxpool.Pool { return p.pool }

// Router is the system-role router, nil with cells off.
func (p *Places) Router() *pgxx.Router {
	if p == nil {
		return nil
	}
	return p.router
}

// CellsOn reports whether any cell is configured: when it is not, nothing here differs from the pooled database.
func (p *Places) CellsOn() bool { return p != nil && p.router != nil && len(p.router.CellNames()) > 0 }

// InCell reports whether the tenant's rows are in a cell. With cells off it never asks the directory.
func (p *Places) InCell(ctx context.Context, id uuid.UUID) (bool, error) {
	if !p.CellsOn() {
		return false, nil
	}
	t, err := p.router.Locate(ctx, id)
	if err != nil {
		return false, err
	}
	return t.Schema != "", nil
}

// Tx runs fn in a transaction of the system role scoped to the tenant, wherever its rows are. It is for work that is only about that tenant. (A nil *Places has no
// pool: it answers OnTenant, InCell and CellsOn only.)
func (p *Places) Tx(ctx context.Context, id uuid.UUID, fn func(pgx.Tx) error) error {
	if !p.CellsOn() {
		return pgxx.InTx(ctx, p.pool, pgx.TxOptions{}, fn)
	}
	return pgxx.InTenantTx(ctx, p.tenants, id, fn)
}

// Read runs fn in a transaction of the system role on the queries that see the tenant's rows, wherever they are, committed when fn returns nil.
func (p *Places) Read(ctx context.Context, id uuid.UUID, fn func(q *sqlc.Queries) error) error {
	return p.Tx(ctx, id, func(tx pgx.Tx) error { return fn(sqlc.New(tx)) })
}

// On runs fn on the connection that sees the tenant's rows. With the tenant in the pooled database that is db, the pool or transaction the caller is working on (so
// the statements and their transaction are the ones the code always had); with the tenant in a cell it is a transaction of its own in the tenant's schema, committed
// when fn returns nil. A tenant that is moving is refused with an error that matches tenantdir.ErrNotServing.
func (p *Places) On(ctx context.Context, id uuid.UUID, db sqlc.DBTX, fn func(db sqlc.DBTX) error) error {
	in, err := p.InCell(ctx, id)
	if err != nil {
		return err
	}
	if !in {
		return fn(db)
	}
	// The tenant's row is read on db, the connection the caller holds, not on a second one from the pool.
	return pgxx.InTenantTx(ctx, p.tenants.reading(db), id, func(tx pgx.Tx) error { return fn(tx) })
}

// OnTenant is On for a caller that wants the queries.
func (p *Places) OnTenant(ctx context.Context, id uuid.UUID, db sqlc.DBTX, fn func(q *sqlc.Queries) error) error {
	return p.On(ctx, id, db, func(db sqlc.DBTX) error { return fn(sqlc.New(db)) })
}

// EachCell runs fn for every tenant placed in a cell that is serving, one after the other, as pgxx.Router.EachReady does: a moving tenant is skipped, a tenant whose fn
// fails does not stop the others, and the errors come back joined. With cells off it does nothing.
func (p *Places) EachCell(ctx context.Context, fn func(ctx context.Context, t pgxx.PlacedTenant) error) error {
	if !p.CellsOn() {
		return nil
	}
	return p.router.EachReady(ctx, fn)
}

// CellTx runs fn in a transaction of the system role in the schema of a tenant placed in a cell: the statements name nothing but the schema (and, in the jobs here,
// the tenant too), and the transaction touches no other tenant.
func (p *Places) CellTx(ctx context.Context, t pgxx.PlacedTenant, fn func(pgx.Tx) error) error {
	return pgxx.InTenantSchemaTx(ctx, t.Pool, t.TenantID, fn)
}

// Source is the tenant directory as plans-svc itself sees it: the tenants table, read on the system pool, cached like every other service caches the answers of
// DirectoryService (so a move is fenced the same way for plans-svc as for the rest).
func Source(systemPool *pgxpool.Pool, o tenantdir.Options) *tenantdir.Directory {
	load := func(ctx context.Context, id uuid.UUID) (tenantdir.Placement, error) {
		r, err := sqlc.New(systemPool).GetPlacement(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return tenantdir.Placement{}, fmt.Errorf("%w: %s", tenantdir.ErrNotFound, id)
		}
		if err != nil {
			return tenantdir.Placement{}, err
		}
		return tenantdir.Placement{TenantID: r.ID, Cell: r.Cell, Region: r.Region, Status: tenantdir.Status(r.PlacementStatus)}, nil
	}
	list := func(ctx context.Context, cell string) ([]tenantdir.Placement, error) {
		var out []tenantdir.Placement
		after := uuid.Nil
		for {
			rows, err := sqlc.New(systemPool).ListPlacements(ctx, sqlc.ListPlacementsParams{Cell: cell, After: after, PageSize: listPage})
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				out = append(out, tenantdir.Placement{TenantID: r.ID, Cell: r.Cell, Region: r.Region, Status: tenantdir.Status(r.PlacementStatus)})
			}
			if len(rows) < listPage {
				return out, nil
			}
			after = rows[len(rows)-1].ID
		}
	}
	return tenantdir.New(load, list, o)
}

// listPage is the page size of the listing of placed tenants.
const listPage = 500
