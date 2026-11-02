//go:build integration

package itest

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/plans-svc/migrations"
)

// Migration 00027 turns the one e-mail provider row of a tenant into its primary and default account, so a tenant that connected Nafru before labels keeps sending
// through it with the key it sealed; then a second account fits beside it, only one is the default, a rule dies with its account, and a language outside the six is refused.
func TestMailAccountsMigrationKeepsTheOneAccountThereWas(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	testkit.MigratedDB(t, pg, "plans", migrations.FS) // creates the roles the cell database is owned by
	db := testkit.CreateCellDB(t, pg, "plans", "mailacct")
	for v := int64(1); v <= 26; v++ {
		if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, v, true); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := pgx.Connect(ctx, db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tenant := uuid.New()
	if _, err := conn.Exec(ctx, `SELECT set_config('app.tenant_id', $1, false)`, tenant.String()); err != nil { // the table forces row-level security, on its owner too
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO tenant_mail_integrations (tenant_id, provider, base_url, api_key_enc, from_address) VALUES ($1, 'nafru', 'https://api.nafru.example', '\x01', 'a@acme.example')`, tenant); err != nil {
		t.Fatal(err)
	}
	if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, 27, true); err != nil {
		t.Fatal(err)
	}
	var key string
	var isDefault bool
	if err := conn.QueryRow(ctx, `SELECT provider, is_default FROM tenant_mail_integrations WHERE tenant_id = $1`, tenant).Scan(&key, &isDefault); err != nil || key != "nafru" || !isDefault {
		t.Fatalf("the account there was: %q default %v, %v", key, isDefault, err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO tenant_mail_integrations (tenant_id, provider, base_url, api_key_enc, from_address) VALUES ($1, 'nafru~eu', 'https://api.nafru.example', '\x02', 'b@acme.eu')`, tenant); err != nil {
		t.Fatalf("a second account of the same provider: %v", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE tenant_mail_integrations SET is_default = true WHERE tenant_id = $1 AND provider = 'nafru~eu'`, tenant); err == nil {
		t.Error("two defaults were stored")
	}
	if _, err := conn.Exec(ctx, `INSERT INTO tenant_mail_integrations (tenant_id, provider, base_url, api_key_enc, from_address) VALUES ($1, 'sendgrid', 'https://x.example', '\x03', 'c@acme.eu')`, tenant); err == nil {
		t.Error("a provider other than nafru was stored")
	}
	if _, err := conn.Exec(ctx, `INSERT INTO tenant_mail_routing (tenant_id, language, account) VALUES ($1, 'de', 'nafru~eu')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO tenant_mail_routing (tenant_id, language, account) VALUES ($1, 'xx', 'nafru~eu')`, tenant); err == nil {
		t.Error("a language outside the six was stored")
	}
	if _, err := conn.Exec(ctx, `INSERT INTO tenant_mail_routing (tenant_id, language, account) VALUES ($1, 'fr', 'nafru~gone')`, tenant); err == nil {
		t.Error("a rule naming an account that is not there was stored")
	}
	if _, err := conn.Exec(ctx, `DELETE FROM tenant_mail_integrations WHERE tenant_id = $1 AND provider = 'nafru~eu'`, tenant); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM tenant_mail_routing WHERE tenant_id = $1`, tenant).Scan(&n); err != nil || n != 0 {
		t.Errorf("a rule outlived the account it named: %d, %v", n, err)
	}
	// Going back keeps the default only.
	if _, err := conn.Exec(ctx, `INSERT INTO tenant_mail_integrations (tenant_id, provider, base_url, api_key_enc, from_address) VALUES ($1, 'nafru~us', 'https://api.nafru.example', '\x04', 'd@acme.com')`, tenant); err != nil {
		t.Fatal(err)
	}
	if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, 27, false); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM tenant_mail_integrations WHERE tenant_id = $1`, tenant).Scan(&n); err != nil || n != 1 {
		t.Errorf("after going back %d accounts remain, want only the default: %v", n, err)
	}
}
