//go:build integration

package sandboxreset_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	aggregatorreset "github.com/0furkancolak/payrudder/services/aggregator/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/aggregator/migrations"
)

// A reset takes a tenant's usage, hour totals, late usage, processed events and period settings, keeps its generation, leaves another tenant alone and finds nothing the second
// time; every table with tenant rows is covered or kept on purpose. It runs for tenants in the pooled database and for tenants in a cell (their own schemas).
func TestResetDeletesTheTenantsUsageAndKeepsItsGeneration(t *testing.T) {
	for _, cells := range []bool{false, true} {
		name := "pooled"
		if cells {
			name = "cells"
		}
		t.Run(name, func(t *testing.T) { testReset(t, cells) })
	}
}

func testReset(t *testing.T, cells bool) {
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
	sbx, other := uuid.New(), uuid.New()
	r := aggregatorreset.New(system)

	var cellSys *pgxpool.Pool
	if cells {
		cell := testkit.CreateCellDB(t, pg, "aggregator", "c1")
		if cellSys, err = pgxx.NewPool(ctx, cell.System); err != nil {
			t.Fatal(err)
		}
		defer cellSys.Close()
		dir := testkit.NewDirectory()
		ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"aggregator_app", "aggregator_system"}}
		for _, tid := range []uuid.UUID{sbx, other} {
			if _, err := pgxx.ProvisionTenant(ctx, cell.Migrator, ts, tid, nil); err != nil {
				t.Fatal(err)
			}
			dir.Place(tid, "c1")
		}
		router, err := pgxx.NewRouter(system, map[string]*pgxpool.Pool{"c1": cellSys}, dir)
		if err != nil {
			t.Fatal(err)
		}
		r = r.WithCells(router)
	}
	// run runs fn as the system role where the tenant's data is.
	run := func(tid uuid.UUID, fn func(tx pgx.Tx) error) {
		t.Helper()
		var err error
		if cells {
			err = pgxx.InTenantSchemaTx(ctx, cellSys, tid, fn)
		} else {
			err = pgxx.InTx(ctx, system, pgx.TxOptions{}, fn)
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, tid := range []uuid.UUID{sbx, other} {
		run(tid, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT ensure_processed_partition('2026-10')`); err != nil {
				return err
			}
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
				if _, err := tx.Exec(ctx, q, tid); err != nil {
					return err
				}
			}
			return nil
		})
	}
	count := func(table string, tid uuid.UUID) int {
		t.Helper()
		var n int
		run(tid, func(tx pgx.Tx) error {
			// The table names are the fixed ones of this test.
			return tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tid).Scan(&n)
		})
		return n
	}
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
	if cells {
		// The pooled database holds nothing of either tenant: a reset of a cell tenant never needed it, and a pooled-only reset would have found nothing.
		for _, tb := range append(append([]string{}, aggregatorreset.Tables...), aggregatorreset.Kept...) {
			var n int
			if err := system.QueryRow(ctx, `SELECT count(*) FROM `+tb).Scan(&n); err != nil || n != 0 {
				t.Errorf("pooled %s has %d rows (%v) in cells mode", tb, n, err)
			}
		}
	}
	missing, err := sandboxreset.MissingTables(ctx, system, aggregatorreset.Tables, aggregatorreset.Kept)
	if err != nil || len(missing) != 0 {
		t.Errorf("tables a reset does not cover: %v, %v", missing, err)
	}
}
