//go:build integration

package sandboxreset_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	aggregatorreset "github.com/0furkancolak/payrudder/services/aggregator/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/aggregator/migrations"
)

// A reset takes a tenant's usage, hour totals, late usage, processed events and period settings, keeps its generation, leaves another tenant alone and finds nothing the second
// time; every table with tenant rows is covered or kept on purpose.
func TestResetDeletesTheTenantsUsageAndKeepsItsGeneration(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.CreateServiceDB(t, pg, "aggregator")
	ctx := context.Background()
	if err := pgxx.Migrate(ctx, db.Migrator, migrations.FS, nil); err != nil {
		t.Fatal(err)
	}
	system, err := pgxx.NewPool(ctx, db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer system.Close()
	if _, err := system.Exec(ctx, `SELECT ensure_processed_partition('2026-10')`); err != nil {
		t.Fatal(err)
	}
	sbx, other := uuid.New(), uuid.New()
	for _, tid := range []uuid.UUID{sbx, other} {
		for _, q := range []string{
			`INSERT INTO usage_aggregates (tenant_id, period_id, customer_id, meter_code, generation, quantity, quantity_scale, event_count, updated_at) VALUES ($1,'2026-10',gen_random_uuid(),'m',1,5,0,1,now())`,
			`INSERT INTO usage_window_aggregates (tenant_id, customer_id, meter_code, generation, hour_id, quantity, quantity_scale, event_count, updated_at) VALUES ($1,gen_random_uuid(),'m',1,'2026-10-05T13',5,0,1,now())`,
			`INSERT INTO usage_distinct_values (tenant_id, customer_id, meter_code, generation, period_id, value_hash) VALUES ($1,gen_random_uuid(),'m',1,'2026-10',sha256('a'::bytea))`,
			`INSERT INTO usage_distinct_hours (tenant_id, customer_id, meter_code, generation, hour_id, value_hash) VALUES ($1,gen_random_uuid(),'m',1,'2026-10-05T13',sha256('a'::bytea))`,
			`INSERT INTO late_usage (tenant_id, billed_period_id, customer_id, meter_code, origin_period_id, generation, quantity, quantity_scale, event_count, updated_at) VALUES ($1,'2026-10',gen_random_uuid(),'m','2026-09',1,5,0,1,now())`,
			`INSERT INTO processed_events (tenant_id, event_id, generation, period_id) VALUES ($1,'e1',1,'2026-10'), ($1,'e2',1,'2026-10')`,
			`INSERT INTO period_settings (tenant_id, period_id, timezone, period_start, period_end, grace_period, cutoff_at) VALUES ($1,'2026-10','UTC',now(),now(),'0',now())`,
			`INSERT INTO aggregate_generations (tenant_id, generation, status) VALUES ($1,1,'active')`,
		} {
			if _, err := system.Exec(ctx, q, tid); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := func(table string, tid uuid.UUID) int {
		t.Helper()
		q := map[string]string{
			"usage_aggregates": `SELECT count(*) FROM usage_aggregates WHERE tenant_id = $1`, "usage_window_aggregates": `SELECT count(*) FROM usage_window_aggregates WHERE tenant_id = $1`, "late_usage": `SELECT count(*) FROM late_usage WHERE tenant_id = $1`,
			"usage_distinct_values": `SELECT count(*) FROM usage_distinct_values WHERE tenant_id = $1`, "usage_distinct_hours": `SELECT count(*) FROM usage_distinct_hours WHERE tenant_id = $1`,
			"processed_events": `SELECT count(*) FROM processed_events WHERE tenant_id = $1`, "period_settings": `SELECT count(*) FROM period_settings WHERE tenant_id = $1`,
			"aggregate_generations": `SELECT count(*) FROM aggregate_generations WHERE tenant_id = $1`,
		}[table]
		var n int
		if err := system.QueryRow(ctx, q, tid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	r := aggregatorreset.New(system)
	deleted, err := r.Reset(ctx, sbx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted["usage_aggregates"] != 1 || deleted["usage_window_aggregates"] != 1 || deleted["usage_distinct_values"] != 1 || deleted["usage_distinct_hours"] != 1 || deleted["late_usage"] != 1 || deleted["processed_events"] != 2 || deleted["period_settings"] != 1 {
		t.Errorf("deleted = %v", deleted)
	}
	for _, tb := range aggregatorreset.Tables {
		if count(tb, sbx) != 0 {
			t.Errorf("%s still has rows of the reset tenant", tb)
		}
		if count(tb, other) == 0 {
			t.Errorf("%s lost another tenant's rows", tb)
		}
	}
	if count("aggregate_generations", sbx) != 1 {
		t.Error("the generation of the tenant was deleted")
	}
	if again, err := r.Reset(ctx, sbx); err != nil || len(again) != 0 {
		t.Errorf("a second reset = %v, %v", again, err)
	}
	missing, err := sandboxreset.MissingTables(ctx, system, aggregatorreset.Tables, aggregatorreset.Kept)
	if err != nil || len(missing) != 0 {
		t.Errorf("tables a reset does not cover: %v, %v", missing, err)
	}
}
