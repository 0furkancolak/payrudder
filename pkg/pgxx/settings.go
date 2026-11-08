package pgxx

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrationSettings are the settings of a service that can apply its own migrations. A service embeds Settings or
// SystemSettings in its config struct (next to config.Base) instead of declaring the fields; the field and variable
// names are the ones every service always had.
type MigrationSettings struct {
	// PGMigratorDSN is the migrator role; it is needed only to migrate.
	PGMigratorDSN string `env:"PAYRUDDER_PG_MIGRATOR_DSN"`
	// MigrateOnStart applies the pending migrations before the service starts.
	MigrateOnStart bool `env:"PAYRUDDER_MIGRATE_ON_START"`
	// PGAdminDSN, with MigrateOnStart, first creates the service's database and roles when they are missing (Provision): a deployment
	// with no init job (Railway). A role that may create roles and databases, reached directly, not through a pooler.
	PGAdminDSN string `env:"PAYRUDDER_PG_ADMIN_DSN"`
}

// CheckMigration is for the service's Validate: migrating needs the migrator DSN.
func (m MigrationSettings) CheckMigration() error {
	if m.MigrateOnStart && m.PGMigratorDSN == "" {
		return errors.New("PAYRUDDER_PG_MIGRATOR_DSN is required with PAYRUDDER_MIGRATE_ON_START")
	}
	return nil
}

// MigrateIfAsked applies the migrations in fsys when MigrateOnStart is set. done reports that the process should exit
// afterwards: migrateOnly (config.Base.MigrateOnly, PAYRUDDER_MIGRATE_ONLY) is for a Kubernetes job that migrates and stops.
func (m MigrationSettings) MigrateIfAsked(ctx context.Context, fsys fs.FS, migrateOnly bool, log *slog.Logger) (done bool, err error) {
	return m.migrateIfAsked(ctx, fsys, migrateOnly, log)
}

// migrateIfAsked is MigrateIfAsked; roleDSNs are the service's other roles, created by Provision next to the migrator's.
func (m MigrationSettings) migrateIfAsked(ctx context.Context, fsys fs.FS, migrateOnly bool, log *slog.Logger, roleDSNs ...string) (done bool, err error) {
	if m.MigrateOnStart {
		if m.PGAdminDSN != "" {
			if err := Provision(ctx, m.PGAdminDSN, m.PGMigratorDSN, roleDSNs...); err != nil {
				return false, err
			}
		}
		if err := Migrate(ctx, m.PGMigratorDSN, fsys, log); err != nil {
			return false, err
		}
	}
	if migrateOnly {
		log.Info("migrations applied; exiting (PAYRUDDER_MIGRATE_ONLY)")
		return true, nil
	}
	return false, nil
}

// SystemSettings are the settings of a service that uses only the system role (consumers and workers).
type SystemSettings struct {
	MigrationSettings
	PGSystemDSN string `env:"PAYRUDDER_PG_SYSTEM_DSN,required"`
}

// MigrateIfAsked is MigrationSettings.MigrateIfAsked; Provision also creates the system role.
func (s SystemSettings) MigrateIfAsked(ctx context.Context, fsys fs.FS, migrateOnly bool, log *slog.Logger) (bool, error) {
	return s.migrateIfAsked(ctx, fsys, migrateOnly, log, s.PGSystemDSN)
}

// Settings are the settings of a service with both pools: the app role (RLS enforced, for request handlers) and the
// system role (BYPASSRLS, for consumers, relays and schedulers).
type Settings struct {
	SystemSettings
	PGAppDSN string `env:"PAYRUDDER_PG_APP_DSN,required"`
}

// MigrateIfAsked is MigrationSettings.MigrateIfAsked; Provision also creates the app and system roles.
func (s Settings) MigrateIfAsked(ctx context.Context, fsys fs.FS, migrateOnly bool, log *slog.Logger) (bool, error) {
	return s.migrateIfAsked(ctx, fsys, migrateOnly, log, s.PGAppDSN, s.PGSystemDSN)
}

// OpenPools opens the app-role and system-role pools; on failure neither stays open.
func (s Settings) OpenPools(ctx context.Context) (appPool, systemPool *pgxpool.Pool, err error) {
	appPool, err = NewPool(ctx, s.PGAppDSN)
	if err != nil {
		return nil, nil, err
	}
	systemPool, err = NewPool(ctx, s.PGSystemDSN)
	if err != nil {
		appPool.Close()
		return nil, nil, err
	}
	return appPool, systemPool, nil
}
