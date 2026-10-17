//go:build integration

package pgxx_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/testkit"
)

var migrations = fstest.MapFS{
	"00001_widgets.sql": {Data: []byte(`
-- +goose Up
CREATE TABLE widgets (tenant_id uuid NOT NULL, id int NOT NULL, PRIMARY KEY (tenant_id, id));
ALTER TABLE widgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE widgets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON widgets
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT ON widgets TO pgxxt_app;
-- +goose Down
DROP TABLE widgets;
`)},
}

func TestRLSHelpers(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	db := testkit.CreateServiceDB(t, pg, "pgxxt")

	if err := pgxx.Migrate(ctx, db.Migrator, migrations, nil); err != nil {
		t.Fatal(err)
	}
	// Running again is a no-op.
	if err := pgxx.Migrate(ctx, db.Migrator, migrations, nil); err != nil {
		t.Fatal(err)
	}

	migrator, err := pgx.Connect(ctx, db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrator.Close(ctx) }()

	t.Run("forced RLS passes", func(t *testing.T) {
		if err := pgxx.VerifyForcedRLS(ctx, migrator, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("deliberately broken tables fail", func(t *testing.T) {
		for _, s := range []string{
			`CREATE TABLE no_rls (tenant_id uuid NOT NULL)`,
			`CREATE TABLE not_forced (tenant_id uuid NOT NULL)`,
			`ALTER TABLE not_forced ENABLE ROW LEVEL SECURITY`,
			`CREATE TABLE no_policy (tenant_id uuid NOT NULL)`,
			`ALTER TABLE no_policy ENABLE ROW LEVEL SECURITY`,
			`ALTER TABLE no_policy FORCE ROW LEVEL SECURITY`,
		} {
			if _, err := migrator.Exec(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		err := pgxx.VerifyForcedRLS(ctx, migrator, nil)
		for _, want := range []string{"no_rls: row level security not enabled", "not_forced: row level security not forced", "no_policy: no RLS policy"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("want error containing %q, got %v", want, err)
			}
		}
		if err := pgxx.VerifyForcedRLS(ctx, migrator, []string{"no_rls", "not_forced", "no_policy"}); err != nil {
			t.Fatalf("allowlisted tables still reported: %v", err)
		}
	})

	app, err := pgxx.NewPool(ctx, db.App)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	a, b := uuid.New(), uuid.New()

	t.Run("InTenantTx isolates tenants", func(t *testing.T) {
		for _, id := range []uuid.UUID{a, b} {
			err := pgxx.InTenantTx(ctx, app, id, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO widgets VALUES ($1, 1)`, id)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		var n int
		err := pgxx.InTenantTx(ctx, app, a, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM widgets`).Scan(&n)
		})
		if err != nil || n != 1 {
			t.Fatalf("tenant A sees %d rows (err %v), want 1", n, err)
		}
		err = pgxx.InTenantTx(ctx, app, a, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO widgets VALUES ($1, 2)`, b)
			return err
		})
		if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
			t.Fatalf("insert for another tenant: %v", err)
		}
	})

	t.Run("no tenant context sees zero rows", func(t *testing.T) {
		var n int
		if err := app.QueryRow(ctx, `SELECT count(*) FROM widgets`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("got %d rows (err %v), want 0", n, err)
		}
		// The setting is transaction-local: a pooled connection doesn't keep it.
		for range 5 {
			if err := app.QueryRow(context.Background(), `SELECT count(*) FROM widgets`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("tenant leaked through the pool: %d rows", n)
			}
		}
	})
}
