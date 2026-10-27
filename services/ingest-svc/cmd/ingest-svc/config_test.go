package main

import (
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/config"
)

// The maintenance interval is a duration the configuration reads once; zero or less (a ticker would panic) is refused when it loads.
func TestPartitionIntervalMustBePositive(t *testing.T) {
	env := map[string]string{"PAYRUDDER_PG_APP_DSN": "postgres://app", "PAYRUDDER_PG_SYSTEM_DSN": "postgres://system", "PAYRUDDER_SERVICE_NAME": "ingest-svc", "PAYRUDDER_KAFKA_BROKERS": "k:9092"}
	cfg, err := config.LoadFrom[Config](env, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PartitionPeriod <= 0 {
		t.Fatalf("the default interval is %v", cfg.PartitionPeriod)
	}
	env["PAYRUDDER_PARTITION_MAINTENANCE_INTERVAL"] = "0s"
	if _, err := config.LoadFrom[Config](env, defaults); err == nil || !strings.Contains(err.Error(), "PAYRUDDER_PARTITION_MAINTENANCE_INTERVAL") {
		t.Fatalf("err = %v, want the interval refused", err)
	}
	env["PAYRUDDER_PARTITION_MAINTENANCE_INTERVAL"] = "soon"
	if _, err := config.LoadFrom[Config](env, defaults); err == nil {
		t.Fatal("a value that is not a duration was accepted")
	}
}

// The embedded defaults name only settings this service has, hold no secret and give no default to a required setting.
func TestConfigDefaults(t *testing.T) {
	if err := config.CheckDefaults[Config](defaults); err != nil {
		t.Fatal(err)
	}
}
