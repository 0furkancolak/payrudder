package pgxx

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx" for goose
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Migrate applies every pending goose migration in fsys (SQL files at its root) using the migrator DSN.
// Migrations merged in a different order than they were deployed (two branches adding one each) are applied too, not refused.
// A Postgres session lock serialises concurrent runners, so several replicas may call it at startup.
func Migrate(ctx context.Context, migratorDSN string, fsys fs.FS, log *slog.Logger) error {
	db, err := sql.Open("pgx", migratorDSN)
	if err != nil {
		return fmt.Errorf("pgxx: open migrator: %w", err)
	}
	defer func() { _ = db.Close() }()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("pgxx: migration locker: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker), goose.WithAllowOutofOrder(true))
	if err != nil {
		return fmt.Errorf("pgxx: migration provider: %w", err)
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("pgxx: migrate up: %w", err)
	}
	for _, r := range results {
		if log != nil {
			log.InfoContext(ctx, "migration applied", "migration_version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
		}
	}
	return nil
}

// MigrateVersion applies (up) or rolls back (down) the one migration of the given version, whatever the others are. It is for tests that check a migration and its Down
// on data; a service never calls it.
func MigrateVersion(ctx context.Context, migratorDSN string, fsys fs.FS, version int64, up bool) error {
	db, err := sql.Open("pgx", migratorDSN)
	if err != nil {
		return fmt.Errorf("pgxx: open migrator: %w", err)
	}
	defer func() { _ = db.Close() }()
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithAllowOutofOrder(true))
	if err != nil {
		return fmt.Errorf("pgxx: migration provider: %w", err)
	}
	if _, err := p.ApplyVersion(ctx, version, up); err != nil {
		return fmt.Errorf("pgxx: migration %d (up=%t): %w", version, up, err)
	}
	return nil
}
