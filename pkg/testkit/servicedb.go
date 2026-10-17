package testkit

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5"
)

// ServiceDB holds DSNs for the three roles of one service database.
type ServiceDB struct {
	Migrator string
	App      string
	System   string
}

var svcName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)

// CreateServiceDB creates <svc>_db with the <svc>_migrator / _app / _system roles, mirroring
// deploy/compose/postgres/01-databases.sh. Call it once per service and test package.
func CreateServiceDB(t testing.TB, pg Postgres, svc string) ServiceDB {
	t.Helper()
	if !svcName.MatchString(svc) {
		t.Fatalf("testkit: invalid service name %q", svc)
	}
	ctx := startCtx(t)
	admin, err := pgx.Connect(ctx, pg.DSN)
	if err != nil {
		t.Fatalf("testkit: connect: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	stmts := []string{
		fmt.Sprintf(`CREATE ROLE %[1]s_migrator LOGIN PASSWORD '%[1]s_migrator_local'`, svc),
		fmt.Sprintf(`CREATE ROLE %[1]s_app LOGIN PASSWORD '%[1]s_app_local'`, svc),
		fmt.Sprintf(`CREATE ROLE %[1]s_system LOGIN PASSWORD '%[1]s_system_local' BYPASSRLS`, svc),
		fmt.Sprintf(`CREATE DATABASE %[1]s_db OWNER %[1]s_migrator`, svc),
		fmt.Sprintf(`REVOKE ALL ON DATABASE %[1]s_db FROM PUBLIC`, svc),
		fmt.Sprintf(`GRANT CONNECT ON DATABASE %[1]s_db TO %[1]s_migrator, %[1]s_app, %[1]s_system`, svc),
	}
	for _, s := range stmts {
		if _, err := admin.Exec(ctx, s); err != nil {
			t.Fatalf("testkit: %s: %v", s, err)
		}
	}

	superInDB := withUserDB(t, pg.DSN, "", "", svc+"_db")
	c, err := pgx.Connect(ctx, superInDB)
	if err != nil {
		t.Fatalf("testkit: connect %s_db: %v", svc, err)
	}
	defer func() { _ = c.Close(ctx) }()
	for _, s := range []string{
		`REVOKE CREATE ON SCHEMA public FROM PUBLIC`,
		fmt.Sprintf(`ALTER SCHEMA public OWNER TO %s_migrator`, svc),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %[1]s_app, %[1]s_system`, svc),
	} {
		if _, err := c.Exec(ctx, s); err != nil {
			t.Fatalf("testkit: %s: %v", s, err)
		}
	}

	db := svc + "_db"
	return ServiceDB{
		Migrator: withUserDB(t, pg.DSN, svc+"_migrator", svc+"_migrator_local", db),
		App:      withUserDB(t, pg.DSN, svc+"_app", svc+"_app_local", db),
		System:   withUserDB(t, pg.DSN, svc+"_system", svc+"_system_local", db),
	}
}

// withUserDB rewrites a postgres:// DSN with another user (empty keeps it) and database.
func withUserDB(t testing.TB, dsn, user, password, db string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("testkit: parse dsn: %v", err)
	}
	if user != "" {
		u.User = url.UserPassword(user, password)
	}
	u.Path = "/" + db
	return u.String()
}

// Ctx returns a context cancelled when the test ends.
func Ctx(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}
