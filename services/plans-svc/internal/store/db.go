// Package store is the way modules reach Postgres: a tenant-scoped transaction on the app pool, over the sqlc queries in store/sqlc.
// Every transaction runs in pgxx.InTenantTx, so row-level security scopes every statement to the caller's tenant.
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// DB is the app pool (role plans_app).
type DB struct{ pool *pgxpool.Pool }

// New returns a DB on the app pool.
func New(appPool *pgxpool.Pool) *DB { return &DB{pool: appPool} }

// Tx runs fn in the caller's tenant transaction.
func (d *DB) Tx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	t, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	return pgxx.InTenantTx(ctx, d.pool, t.ID, func(tx pgx.Tx) error { return fn(sqlc.New(tx)) })
}
