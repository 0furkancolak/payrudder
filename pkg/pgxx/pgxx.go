// Package pgxx holds Postgres helpers shared by every service: traced pools, the tenant-scoped transaction helper
// that RLS depends on, a goose migration runner and the forced-RLS check used in CI.
//
// Each service opens two pools ([06-multi-tenancy.md]): an app pool (role <svc>_app, RLS enforced) for request
// handlers and a system pool (role <svc>_system, BYPASSRLS) for consumers, relays and schedulers.
package pgxx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultMaxConns and DefaultMaxConnIdleTime keep a service's two pools small. pgx would otherwise size each pool by CPU count (a 16-core node gives
// 16 connections per pool, each a Postgres backend and a few hundred KiB here), and keep idle connections for 30 minutes. A service that needs more
// sets pool_max_conns in its DSN (or raises it per tenant tier); the defaults are for a lightly loaded one.
const (
	DefaultMaxConns        = 4
	DefaultMaxConnIdleTime = time.Minute
)

// NewPool opens a pool with OpenTelemetry tracing and pings it.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxx: parse dsn: %w", err)
	}
	if !strings.Contains(dsn, "pool_max_conns") {
		cfg.MaxConns = DefaultMaxConns
	}
	if !strings.Contains(dsn, "pool_max_conn_idle_time") {
		cfg.MaxConnIdleTime = DefaultMaxConnIdleTime
	}
	// Query parameters are deliberately not recorded: they include key hashes, e-mails and tenant data.
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxx: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgxx: ping: %w", err)
	}
	return pool, nil
}

// Beginner is satisfied by *pgxpool.Pool and pgx.Tx.
type Beginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// InTenantTx runs fn in a transaction scoped to tenantID. In the pooled database it first sets app.tenant_id transaction-locally
// (set_config(..., true)), so RLS policies see the tenant and a pooled connection never carries it into the next request.
// When db is a Router (cells mode, ADR 0018) the placement of the tenant decides where the transaction runs and what scopes it: a tenant in a cell also gets
// SET LOCAL search_path to its schema. It is the only way request handlers open a transaction.
func InTenantTx(ctx context.Context, db Beginner, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	if tenantID == uuid.Nil {
		return errors.New("pgxx: InTenantTx with nil tenant")
	}
	if tb, ok := db.(TenantBeginner); ok {
		err := runTx(ctx, func() (pgx.Tx, error) { return tb.BeginTenantTx(ctx, tenantID, pgx.TxOptions{}) }, fn)
		if err != nil && isMissingSchema(err) {
			// The directory sent us to a place the tenant is not: ask it again next time (a move, an answer older than the move).
			tb.Invalidate(tenantID)
		}
		return err
	}
	return InTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return fmt.Errorf("pgxx: set tenant: %w", err)
		}
		return fn(tx)
	})
}

// InTx runs fn in a transaction, committing on nil and rolling back otherwise (also on panic).
func InTx(ctx context.Context, db Beginner, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	return runTx(ctx, func() (pgx.Tx, error) { return db.BeginTx(ctx, opts) }, fn)
}

func runTx(ctx context.Context, begin func() (pgx.Tx, error), fn func(pgx.Tx) error) (err error) {
	tx, err := begin()
	if err != nil {
		return fmt.Errorf("pgxx: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SQLSTATE codes the services map to gRPC codes.
const (
	CodeUniqueViolation     = "23505"
	CodeExclusionViolation  = "23P01"
	CodeForeignKeyViolation = "23503"
	CodeCheckViolation      = "23514"
	CodeInsufficientPriv    = "42501" // also raised by RLS WITH CHECK failures
	CodeRaiseException      = "P0001"
)

// ErrCode returns the SQLSTATE of a Postgres error, or "".
func ErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// ConstraintName returns the violated constraint of a Postgres error, or "".
func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}
