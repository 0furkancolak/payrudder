package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// DB is what the stores run their statements on: the system pool, or the system-role pgxx.Router when cells are on (ADR 0018). Every statement of a store is about
// one tenant and runs where that tenant's data is: on the pool itself for the pooled database (as it always did), and in a transaction whose search path is the
// tenant's schema for a tenant placed in a cell. A transaction is never shared between tenants: it is opened for one and ends with it.
//
// payments-worker has no row-level security on any table (only payments_system touches them, and every statement names its tenant), so in a cell the schema
// is the whole boundary. That is why a store method takes the tenant it acts for and cannot be asked for another's place.
type DB = pgxx.Beginner

// ErrNoTenant is a statement that would run without naming a tenant on a database that has cells: it could only be answered from the pooled database, which is the
// wrong place for a tenant in a cell.
var ErrNoTenant = errors.New("store: this statement names no tenant, and cells are on")

// at runs fn with the queries of the place tenant's data is in. A nil tenant is the pooled database (a statement about no tenant: a notification whose tenant is not
// known yet); with cells off every tenant is there.
func at(ctx context.Context, db DB, tenant uuid.UUID, fn func(q *sqlc.Queries) error) error {
	switch d := db.(type) {
	case *pgxpool.Pool:
		return fn(sqlc.New(d))
	case *pgxx.Router:
		if tenant == uuid.Nil {
			return fn(sqlc.New(d.Pooled()))
		}
		t, err := d.Locate(ctx, tenant)
		if err != nil {
			return err
		}
		if t.Schema == "" {
			return fn(sqlc.New(t.Pool))
		}
		return pgxx.InTenantTx(ctx, d, tenant, func(tx pgx.Tx) error { return fn(sqlc.New(tx)) })
	}
	return pgxx.InTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error { return fn(sqlc.New(tx)) })
}

// Tx runs fn in one transaction on the place of the tenant's data, for work that needs more than one statement to be atomic. fn gets the transaction, the
// queries on it, and the tenant's schema when the tenant is in a cell ("" in the pooled database, where a statement must name the tenant itself). The transaction
// belongs to the one tenant: it is opened for it and ends with it.
func Tx(ctx context.Context, db DB, tenant uuid.UUID, fn func(tx pgx.Tx, q *sqlc.Queries, schema string) error) error {
	pooled := func(p pgxx.Beginner) error {
		return pgxx.InTx(ctx, p, pgx.TxOptions{}, func(t pgx.Tx) error { return fn(t, sqlc.New(t), "") })
	}
	switch d := db.(type) {
	case *pgxpool.Pool:
		return pooled(d)
	case *pgxx.Router:
		if tenant == uuid.Nil {
			return pooled(d.Pooled())
		}
		t, err := d.Locate(ctx, tenant)
		if err != nil {
			return err
		}
		if t.Schema == "" {
			return pooled(t.Pool)
		}
		return pgxx.InTenantTx(ctx, d, tenant, func(tx pgx.Tx) error { return fn(tx, sqlc.New(tx), t.Schema) })
	}
	return pooled(db)
}

// tx is Tx for the stores, which do not need the schema.
func tx(ctx context.Context, db DB, tenant uuid.UUID, fn func(tx pgx.Tx, q *sqlc.Queries) error) error {
	return Tx(ctx, db, tenant, func(t pgx.Tx, q *sqlc.Queries, _ string) error { return fn(t, q) })
}

// get is at for a query that returns a value.
func get[T any](ctx context.Context, db DB, tenant uuid.UUID, fn func(q *sqlc.Queries) (T, error)) (T, error) {
	var v T
	err := at(ctx, db, tenant, func(q *sqlc.Queries) (err error) {
		v, err = fn(q)
		return err
	})
	return v, err
}

// lookup is get for a query that finds one row or none: the value, whether it was found, and any other error.
func lookup[T any](ctx context.Context, db DB, tenant uuid.UUID, fn func(q *sqlc.Queries) (T, error)) (T, bool, error) {
	v, err := get(ctx, db, tenant, fn)
	return found(v, err)
}

// hasCells reports whether db routes tenants to cells.
func hasCells(db DB) bool {
	r, ok := db.(*pgxx.Router)
	return ok && len(r.CellNames()) > 0
}

// requireTenant refuses a statement that names no tenant when cells are on.
func requireTenant(db DB, tenant uuid.UUID) error {
	if tenant == uuid.Nil && hasCells(db) {
		return ErrNoTenant
	}
	return nil
}
