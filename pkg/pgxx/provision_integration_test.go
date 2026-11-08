//go:build integration

package pgxx_test

import (
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// TestProvision creates a service's database and roles from nothing, as a Railway deployment without an init job does, twice.
func TestProvision(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	dsn := func(user, password, db string) string {
		u, err := url.Parse(pg.DSN)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(user, password)
		u.Path = "/" + db
		return u.String()
	}
	migrator := dsn("prov_migrator", "m1gr4tor", "prov_db")
	app := dsn("prov_app", "it's-app", "prov_db") // a quote in the password must survive
	system := dsn("prov_system", "syst3m", "prov_db")

	for range 2 {
		if err := pgxx.Provision(ctx, pg.DSN, migrator, app, system); err != nil {
			t.Fatal(err)
		}
	}

	admin, err := pgx.Connect(ctx, pg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	var owner string
	if err := admin.QueryRow(ctx, "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'prov_db'").Scan(&owner); err != nil || owner != "prov_migrator" {
		t.Fatalf("owner of prov_db = %q, %v", owner, err)
	}
	for role, want := range map[string]bool{"prov_migrator": false, "prov_app": false, "prov_system": true} {
		var bypass bool
		if err := admin.QueryRow(ctx, "SELECT rolbypassrls FROM pg_roles WHERE rolname = $1", role).Scan(&bypass); err != nil || bypass != want {
			t.Errorf("%s bypassrls = %v (%v), want %v", role, bypass, err, want)
		}
	}

	for _, d := range []string{migrator, app, system} {
		c, err := pgx.Connect(ctx, d)
		if err != nil {
			t.Fatalf("connect %s: %v", d, err)
		}
		_ = c.Close(ctx)
	}
	// A service that does not connect as one of its database's roles still gets it, NOLOGIN, for the migrations' grants.
	if err := pgxx.Provision(ctx, pg.DSN, dsn("solo_migrator", "s0lo", "solo_db"), dsn("solo_app", "s0lo-app", "solo_db")); err != nil {
		t.Fatal(err)
	}
	var login, bypass bool
	if err := admin.QueryRow(ctx, "SELECT rolcanlogin, rolbypassrls FROM pg_roles WHERE rolname = 'solo_system'").Scan(&login, &bypass); err != nil || login || !bypass {
		t.Errorf("solo_system: login=%v bypassrls=%v (%v), want a NOLOGIN BYPASSRLS role", login, bypass, err)
	}

	c, err := pgx.Connect(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, "CREATE TABLE nope (id int)"); err == nil {
		t.Error("the app role may create tables in public")
	}
	m, err := pgx.Connect(ctx, migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close(ctx) }()
	if _, err := m.Exec(ctx, "CREATE TABLE yes (id int)"); err != nil {
		t.Errorf("the migrator cannot create tables in public: %v", err)
	}
}
