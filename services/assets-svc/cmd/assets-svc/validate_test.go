package main

import (
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/config"
)

// Migrating without the migrator DSN is refused when the configuration loads, like in every other service, not after start-up.
func TestMigrateOnStartNeedsTheMigratorDSN(t *testing.T) {
	env := map[string]string{
		"PAYRUDDER_PG_APP_DSN": "postgres://app", "PAYRUDDER_PG_SYSTEM_DSN": "postgres://system",
		"PAYRUDDER_SERVICE_NAME": "assets-svc", "S3_ACCESS_KEY": "k", "S3_SECRET_KEY": "s", "PAYRUDDER_MIGRATE_ON_START": "true",
	}
	_, err := config.LoadFrom[Config](env, defaults)
	if err == nil || !strings.Contains(err.Error(), "PAYRUDDER_PG_MIGRATOR_DSN") {
		t.Fatalf("err = %v, want the missing migrator DSN", err)
	}
	env["PAYRUDDER_PG_MIGRATOR_DSN"] = "postgres://migrator"
	cfg, err := config.LoadFrom[Config](env, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PGAppDSN != "postgres://app" || cfg.PGSystemDSN != "postgres://system" || !cfg.MigrateOnStart {
		t.Fatalf("the embedded settings did not load: %+v", cfg.Settings)
	}
}
