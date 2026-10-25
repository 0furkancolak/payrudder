package pgxx

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCheckMigration(t *testing.T) {
	if err := (MigrationSettings{}).CheckMigration(); err != nil {
		t.Errorf("not migrating needs no migrator DSN: %v", err)
	}
	if err := (MigrationSettings{MigrateOnStart: true, PGMigratorDSN: "postgres://m"}).CheckMigration(); err != nil {
		t.Errorf("migrating with a DSN: %v", err)
	}
	err := (MigrationSettings{MigrateOnStart: true}).CheckMigration()
	if err == nil || !strings.Contains(err.Error(), "PAYRUDDER_PG_MIGRATOR_DSN") {
		t.Errorf("migrating without a DSN: err = %v", err)
	}
}

func TestMigrateIfAskedWithoutMigrating(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if done, err := (MigrationSettings{}).MigrateIfAsked(context.Background(), fstest.MapFS{}, false, log); done || err != nil {
		t.Errorf("a service that does not migrate: done=%v err=%v", done, err)
	}
	if done, err := (MigrationSettings{}).MigrateIfAsked(context.Background(), fstest.MapFS{}, true, log); !done || err != nil {
		t.Errorf("migrate-only without migrating still stops the process: done=%v err=%v", done, err)
	}
}
