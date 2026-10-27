package pgxx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"regexp"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// TenantSchemas is what a service needs to put its tables into a tenant schema: the tenant migration set and the roles that use them.
//
// The migrations are goose SQL like the service's own, but written for a schema, not for public: they create only the tables a tenant owns (every table with a
// tenant_id), name no schema, and make their functions bind the schema they were created in (SET search_path FROM CURRENT), so the same files build t_<id> for
// any tenant. They are run with the search path set to the tenant's schema, and each schema keeps its own goose_db_version, which is what makes a run resumable
// and a second run a no-op. A test per service compares the result with the service's public schema (SchemaShape), so the two sets cannot drift apart.
type TenantSchemas struct {
	// Migrations holds the SQL files at its root.
	Migrations fs.FS
	// Roles are granted USAGE on every tenant schema (the app and system roles of the service). Table privileges are the migrations' own GRANTs.
	Roles []string
}

func (ts TenantSchemas) check() error {
	if ts.Migrations == nil {
		return errors.New("pgxx: tenant schemas without migrations")
	}
	if len(ts.Roles) == 0 {
		return errors.New("pgxx: tenant schemas without roles")
	}
	return nil
}

// EnsureShared creates the shared schema of a cell database and lets the roles use it. It is idempotent.
func EnsureShared(ctx context.Context, migratorDSN string, roles []string) error {
	conn, err := pgx.Connect(ctx, migratorDSN)
	if err != nil {
		return fmt.Errorf("pgxx: connect migrator: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return ensureSchema(ctx, conn, SharedSchema, roles)
}

func ensureSchema(ctx context.Context, conn *pgx.Conn, schema string, roles []string) error {
	if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoteIdent(schema)); err != nil {
		return fmt.Errorf("pgxx: create schema %s: %w", schema, err)
	}
	for _, role := range roles {
		if _, err := conn.Exec(ctx, "GRANT USAGE ON SCHEMA "+quoteIdent(schema)+" TO "+quoteIdent(role)); err != nil {
			return fmt.Errorf("pgxx: grant usage on %s to %s: %w", schema, role, err)
		}
	}
	return nil
}

// ProvisionTenant creates the tenant's schema in the cell database migratorDSN points at (if it is not there), lets the roles use it, and applies the tenant
// migrations. A run that stopped half way (a migration failed, the process died) is continued by running it again; a finished one does nothing. It returns the
// number of migrations it applied.
func ProvisionTenant(ctx context.Context, migratorDSN string, ts TenantSchemas, tenantID uuid.UUID, log *slog.Logger) (int, error) {
	if err := ts.check(); err != nil {
		return 0, err
	}
	conn, err := pgx.Connect(ctx, migratorDSN)
	if err != nil {
		return 0, fmt.Errorf("pgxx: connect migrator: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if err := ensureSchema(ctx, conn, SharedSchema, ts.Roles); err != nil {
		return 0, err
	}
	if err := ensureSchema(ctx, conn, tenantdir.Schema(tenantID), ts.Roles); err != nil {
		return 0, err
	}
	return migrateSchema(ctx, migratorDSN, ts, tenantID, log)
}

// TenantMigrationReport is the outcome of MigrateTenants.
type TenantMigrationReport struct {
	// Schemas is how many tenant schemas the database has.
	Schemas int
	// Applied counts the migrations applied per schema that was behind.
	Applied map[uuid.UUID]int
	// Failed holds the schemas whose migration stopped, with the error. The others were migrated anyway.
	Failed map[uuid.UUID]error
}

// Err is nil when no schema failed.
func (r TenantMigrationReport) Err() error {
	if len(r.Failed) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(r.Failed))
	for id := range r.Failed {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	errs := make([]error, len(ids))
	for i, id := range ids {
		errs[i] = fmt.Errorf("%s: %w", tenantdir.Schema(id), r.Failed[id])
	}
	return fmt.Errorf("pgxx: %d of %d tenant schemas failed to migrate: %w", len(ids), r.Schemas, errors.Join(errs...))
}

// MigrateTenants applies the pending tenant migrations to every tenant schema of the cell database. A schema that fails does not stop the others (a broken
// tenant must not hold the rest back); the failures are in the report and in its Err. It is safe to run from several processes at once (a lock per schema),
// to run again after a failure (each schema continues where it stopped) and to run when nothing is pending (nothing happens).
func MigrateTenants(ctx context.Context, migratorDSN string, ts TenantSchemas, log *slog.Logger) (TenantMigrationReport, error) {
	rep := TenantMigrationReport{Applied: map[uuid.UUID]int{}, Failed: map[uuid.UUID]error{}}
	if err := ts.check(); err != nil {
		return rep, err
	}
	conn, err := pgx.Connect(ctx, migratorDSN)
	if err != nil {
		return rep, fmt.Errorf("pgxx: connect migrator: %w", err)
	}
	ids, err := TenantSchemaIDs(ctx, conn)
	_ = conn.Close(context.WithoutCancel(ctx))
	if err != nil {
		return rep, err
	}
	rep.Schemas = len(ids)
	for _, id := range ids {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		n, err := migrateSchema(ctx, migratorDSN, ts, id, log)
		switch {
		case err != nil:
			rep.Failed[id] = err
			if log != nil {
				log.ErrorContext(ctx, "tenant schema migration failed", "tenant_id", id, "error", err)
			}
		case n > 0:
			rep.Applied[id] = n
		}
	}
	return rep, rep.Err()
}

// migrateSchema runs goose on one tenant schema: a connection whose search path is the schema, the version table in it, and a lock of its own.
func migrateSchema(ctx context.Context, migratorDSN string, ts TenantSchemas, tenantID uuid.UUID, log *slog.Logger) (int, error) {
	schema := tenantdir.Schema(tenantID)
	cfg, err := pgx.ParseConfig(migratorDSN)
	if err != nil {
		return 0, fmt.Errorf("pgxx: parse migrator dsn: %w", err)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	// Only the schema: what a migration creates lands in it, and what a function binds (SET search_path FROM CURRENT) is the schema alone.
	cfg.RuntimeParams["search_path"] = quoteIdent(schema)
	connStr := stdlib.RegisterConnConfig(cfg)
	defer stdlib.UnregisterConnConfig(connStr)
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return 0, fmt.Errorf("pgxx: open migrator: %w", err)
	}
	defer func() { _ = db.Close() }()

	h := fnv.New64a()
	_, _ = h.Write([]byte("payrudder tenant schema " + schema))
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(int64(h.Sum64() >> 1)))
	if err != nil {
		return 0, fmt.Errorf("pgxx: migration locker: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, ts.Migrations, goose.WithSessionLocker(locker), goose.WithAllowOutofOrder(true))
	if err != nil {
		return 0, fmt.Errorf("pgxx: migration provider: %w", err)
	}
	results, err := p.Up(ctx)
	for _, r := range results {
		if log != nil {
			log.InfoContext(ctx, "tenant migration applied", "tenant_id", tenantID, "migration_version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
		}
	}
	if err != nil {
		return len(results), fmt.Errorf("pgxx: migrate %s: %w", schema, err)
	}
	return len(results), nil
}

// TenantSchemaIDs lists the tenants that have a schema in the database q is connected to, in id order.
func TenantSchemaIDs(ctx context.Context, q Querier) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname ~ '^t_[0-9a-f]{32}$' ORDER BY nspname`)
	if err != nil {
		return nil, fmt.Errorf("pgxx: list tenant schemas: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("pgxx: list tenant schemas: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(names))
	for _, n := range names {
		if id, ok := tenantdir.TenantOfSchema(n); ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// LatestTenantVersion is the highest version in the tenant migration set.
func LatestTenantVersion(migrations fs.FS) (int64, error) {
	entries, err := fs.ReadDir(migrations, ".")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, e := range entries {
		m := migrationFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0, err
		}
		latest = max(latest, v)
	}
	return latest, nil
}

var migrationFile = regexp.MustCompile(`^(\d+)_.*\.sql$`)

// TenantSchemaVersion is the highest migration applied in the tenant's schema; ok is false when the schema or its version table is not there.
func TenantSchemaVersion(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, tenantID uuid.UUID) (version int64, ok bool, err error) {
	schema := tenantdir.Schema(tenantID)
	var reg *string
	if err := q.QueryRow(ctx, `SELECT to_regclass($1)::text`, quoteIdent(schema)+".goose_db_version").Scan(&reg); err != nil {
		return 0, false, err
	}
	if reg == nil {
		return 0, false, nil
	}
	err = q.QueryRow(ctx, `SELECT COALESCE(max(version_id), 0) FROM `+quoteIdent(schema)+`.goose_db_version WHERE is_applied`).Scan(&version)
	return version, err == nil, err
}
