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

// Migration 00017 gives every tenant that exists the language the fixed map of billing-svc gave it (docs/plans/05): Turkey tr, Germany and Austria de, France fr, Spain es,
// anything else English (empty), so no tenant's documents change language the day the setting appears.
func TestDefaultLanguageMigrationKeepsWhatTheCountryMapGave(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	testkit.MigratedDB(t, pg, "plans", migrations.FS) // creates the roles the cell database is owned by
	db := testkit.CreateCellDB(t, pg, "plans", "lang")
	for v := int64(1); v <= 16; v++ {
		if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, v, true); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := pgx.Connect(ctx, db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	account := uuid.New()
	if _, err := conn.Exec(ctx, `INSERT INTO accounts (id, name) VALUES ($1, 'Acme')`, account); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"TR": "tr", "tr": "tr", "DE": "de", "AT": "de", "FR": "fr", "ES": "es", "GB": "", "SA": "", "": ""}
	ids := map[string]uuid.UUID{}
	i := 0
	for country := range want {
		i++
		app, tenant := uuid.New(), uuid.New()
		ids[country] = tenant
		if _, err := conn.Exec(ctx, `INSERT INTO apps (id, account_id, name, slug) VALUES ($1, $2, 'App', $3)`, app, account, "app"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
		address := `{}`
		if country != "" {
			address = `{"country":"` + country + `"}`
		}
		if _, err := conn.Exec(ctx, `INSERT INTO tenants (id, account_id, environment, name, tier, currency, timezone, grace_period, app_id, address, status)
			VALUES ($1, $2, 'sandbox', 'T', 'free', 'USD', 'UTC', interval '0', $3, $4::jsonb, 'active')`, tenant, account, app, address); err != nil {
			t.Fatal(err)
		}
	}
	if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, 17, true); err != nil {
		t.Fatal(err)
	}
	for country, lang := range want {
		var got string
		if err := conn.QueryRow(ctx, `SELECT default_language FROM tenants WHERE id = $1`, ids[country]).Scan(&got); err != nil || got != lang {
			t.Errorf("a seller in %q: default language %q, %v; want %q", country, got, err, lang)
		}
	}
	// Only the six languages, or empty.
	if _, err := conn.Exec(ctx, `UPDATE tenants SET default_language = 'xx' WHERE id = $1`, ids["GB"]); err == nil {
		t.Error("a language outside the six was stored")
	}
}
