//go:build integration

package sandboxreset_test

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/chmigrate"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/analytics-svc/migrations"
)

// A reset takes a tenant's usage (events and the hourly roll-up the materialized view fills), limit events and invoice facts from ClickHouse, waits for the
// deletion, leaves another tenant alone and finds nothing the second time.
func TestResetDeletesTheTenantsRowsFromEveryTable(t *testing.T) {
	ch := testkit.StartClickHouse(t)
	conn, err := clickhouse.Open(chmigrate.Options(ch.Addr, ch.Database, ch.User, ch.Password))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if err = chmigrate.Run(ctx, conn, migrations.FS); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatal(err)
	}
	sbx, other := uuid.New(), uuid.New()
	at := time.Now().UTC().Truncate(time.Hour)
	for _, tid := range []uuid.UUID{sbx, other} {
		for _, q := range [][]any{
			{`INSERT INTO usage_events (tenant_id, event_id, customer_id, customer_external_id, meter_code, quantity_units, quantity_scale, period_id, event_time, ingested_at) VALUES (?, 'e1', ?, 'c', 'm', 5, 0, '2026-10', ?, ?)`, tid, uuid.New(), at, at},
			{`INSERT INTO limit_events (tenant_id, event_id, customer_id, meter_code, period_id, threshold_pct, limit_units, used_units, quantity_scale, detected_at) VALUES (?, 'l1', ?, 'm', '2026-10', 80, 10, 8, 0, ?)`, tid, uuid.New(), at},
			{`INSERT INTO invoice_facts (tenant_id, invoice_id, version, customer_id, period_id, kind, total_minor, currency, number, occurred_at) VALUES (?, ?, 1, ?, '2026-10', 'REGULAR', 100, 'USD', 'SBX-1', ?)`, tid, uuid.New(), uuid.New(), at},
		} {
			if err := conn.Exec(ctx, q[0].(string), q[1:]...); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := func(table string, tid uuid.UUID) uint64 {
		t.Helper()
		q := map[string]string{"usage_events": `SELECT count() FROM usage_events WHERE tenant_id = ?`, "usage_hourly": `SELECT count() FROM usage_hourly WHERE tenant_id = ?`,
			"limit_events": `SELECT count() FROM limit_events WHERE tenant_id = ?`, "invoice_facts": `SELECT count() FROM invoice_facts WHERE tenant_id = ?`}[table]
		var n uint64
		if err := conn.QueryRow(ctx, q, tid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, tb := range sandboxreset.Tables() {
		if count(tb, sbx) == 0 {
			t.Fatalf("the fixture left %s empty", tb)
		}
	}
	r := sandboxreset.New(conn)
	deleted, err := r.Reset(ctx, sbx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range sandboxreset.Tables() {
		if deleted[tb] == 0 {
			t.Errorf("deleted[%s] = 0", tb)
		}
		if n := count(tb, sbx); n != 0 {
			t.Errorf("%s still has %d rows of the reset tenant (the deletion must be finished when Reset returns)", tb, n)
		}
		if n := count(tb, other); n == 0 {
			t.Errorf("%s lost another tenant's rows", tb)
		}
	}
	if again, err := r.Reset(ctx, sbx); err != nil || len(again) != 0 {
		t.Errorf("a second reset = %v, %v", again, err)
	}
}
