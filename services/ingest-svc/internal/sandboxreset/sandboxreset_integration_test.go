//go:build integration

package sandboxreset_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	ingestreset "github.com/0furkancolak/payrudder/services/ingest-svc/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/ingest-svc/migrations"
)

// A reset takes one tenant's dedup keys and unpublished events and nothing of another tenant's, and a second reset finds nothing; every table with tenant rows is covered.
func TestResetDeletesOnlyTheTenantsKeysAndEvents(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.CreateServiceDB(t, pg, "ingest")
	ctx := context.Background()
	if err := pgxx.Migrate(ctx, db.Migrator, migrations.FS, nil); err != nil {
		t.Fatal(err)
	}
	app, err := pgxx.NewPool(ctx, db.App)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	system, err := pgxx.NewPool(ctx, db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer system.Close()
	now := time.Now().UTC()
	if _, err := system.Exec(ctx, `SELECT maintain_idempotency_partitions($1, 1, interval '90 days')`, now); err != nil {
		t.Fatal(err)
	}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	sbx, other := uuid.New(), uuid.New()
	for _, tid := range []uuid.UUID{sbx, other} {
		if err := pgxx.InTenantTx(ctx, app, tid, func(tx pgx.Tx) error {
			for _, key := range []string{"k1", "k2", "k3"} {
				if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (tenant_id, idempotency_key, event_id, payload_hash, created_at) VALUES (app_tenant_id(), $1, 'e', '\x00', $2)`, key, monthStart); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, `SELECT enqueue_outbox(app_tenant_id(), 'usage', 'k', ARRAY['\x01'::bytea, '\x02'::bytea], ARRAY['{}', '{}'], now())`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(table string, tid uuid.UUID) int {
		t.Helper()
		var n int
		q := map[string]string{"idempotency_keys": `SELECT count(*) FROM idempotency_keys WHERE tenant_id = $1`, "outbox": `SELECT count(*) FROM outbox WHERE tenant_id = $1`}[table]
		if err := system.QueryRow(ctx, q, tid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	r := ingestreset.New(system)
	deleted, err := r.Reset(ctx, sbx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted["idempotency_keys"] != 3 || deleted["outbox"] != 2 {
		t.Errorf("deleted = %v", deleted)
	}
	if count("idempotency_keys", sbx) != 0 || count("outbox", sbx) != 0 {
		t.Error("the reset tenant still has rows")
	}
	if count("idempotency_keys", other) != 3 || count("outbox", other) != 2 {
		t.Error("another tenant's rows were deleted")
	}
	if again, err := r.Reset(ctx, sbx); err != nil || len(again) != 0 {
		t.Errorf("a second reset = %v, %v", again, err)
	}
	missing, err := sandboxreset.MissingTables(ctx, system, ingestreset.Tables, ingestreset.Kept)
	if err != nil || len(missing) != 0 {
		t.Errorf("tables a reset does not cover: %v, %v", missing, err)
	}
}
