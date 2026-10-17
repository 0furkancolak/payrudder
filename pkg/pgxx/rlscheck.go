package pgxx

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Querier is satisfied by *pgxpool.Pool, *pgx.Conn and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// TenantTables lists every table in the public schema that has a tenant_id column, excluding allowlisted ones.
func TenantTables(ctx context.Context, q Querier, allow []string) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		ORDER BY c.relname`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(names, func(n string) bool { return slices.Contains(allow, n) }), nil
}

// VerifyForcedRLS fails for every table with a tenant_id column that lacks ENABLE + FORCE ROW LEVEL SECURITY or a
// policy. Tables in allow are skipped (they must be justified in the data-model doc). Every service runs this in CI.
func VerifyForcedRLS(ctx context.Context, q Querier, allow []string) error {
	tables, err := TenantTables(ctx, q, allow)
	if err != nil {
		return err
	}
	rows, err := q.Query(ctx, `
		SELECT c.relname, c.relrowsecurity, c.relforcerowsecurity,
		       EXISTS (SELECT 1 FROM pg_policies p WHERE p.schemaname = 'public' AND p.tablename = c.relname)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = ANY($1)`, tables)
	if err != nil {
		return err
	}
	type state struct {
		name                  string
		enabled, forced, poly bool
	}
	states, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (state, error) {
		var s state
		err := r.Scan(&s.name, &s.enabled, &s.forced, &s.poly)
		return s, err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, s := range states {
		switch {
		case !s.enabled:
			errs = append(errs, fmt.Errorf("table %s: row level security not enabled", s.name))
		case !s.forced:
			errs = append(errs, fmt.Errorf("table %s: row level security not forced", s.name))
		case !s.poly:
			errs = append(errs, fmt.Errorf("table %s: no RLS policy", s.name))
		}
	}
	return errors.Join(errs...)
}
