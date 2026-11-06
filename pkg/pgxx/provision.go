package pgxx

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Provision creates the service's database and login roles when they are missing, with the layout of
// deploy/compose/postgres/01-databases.sh (docs/06-multi-tenancy.md): the database of migratorDSN, owned by its user, and one role per
// DSN, with the DSN's password. A role whose name ends in _system gets BYPASSRLS, every other one NOBYPASSRLS. Running it again sets the
// passwords and attributes to the current ones and changes nothing else. Every database has the three roles <svc>_migrator, <svc>_app and
// <svc>_system, whose names the migrations grant to: one this service does not connect as is created NOLOGIN, with no password. adminDSN is a role that may CREATE ROLE ... BYPASSRLS and
// CREATE DATABASE (Railway's postgres user); it must reach Postgres directly, not through a transaction pooler.
func Provision(ctx context.Context, adminDSN, migratorDSN string, roleDSNs ...string) error {
	owner, err := pgx.ParseConfig(migratorDSN)
	if err != nil {
		return fmt.Errorf("pgxx: provision: migrator dsn: %w", err)
	}
	if owner.Database == "" || owner.User == "" || owner.Password == "" {
		return errors.New("pgxx: provision: the migrator dsn needs a database, a user and a password")
	}
	roles := []*pgx.ConnConfig{owner}
	for _, dsn := range roleDSNs {
		c, err := pgx.ParseConfig(dsn)
		if err != nil {
			return fmt.Errorf("pgxx: provision: role dsn: %w", err)
		}
		if c.User == "" || c.Password == "" {
			return errors.New("pgxx: provision: every role dsn needs a user and a password")
		}
		roles = append(roles, c)
	}

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("pgxx: provision: connect admin: %w", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	names := make([]string, 0, len(roles))
	for _, r := range roles {
		attrs := "NOBYPASSRLS"
		if r != owner && strings.HasSuffix(r.User, "_system") {
			attrs = "BYPASSRLS"
		}
		var exists bool
		if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)", r.User).Scan(&exists); err != nil {
			return fmt.Errorf("pgxx: provision: role %s: %w", r.User, err)
		}
		if !exists {
			if _, err := admin.Exec(ctx, "CREATE ROLE "+quoteIdent(r.User)); err != nil {
				return fmt.Errorf("pgxx: provision: create role %s: %w", r.User, err)
			}
		}
		// A password cannot be a bind parameter of ALTER ROLE: quote it as a literal.
		if _, err := admin.Exec(ctx, "ALTER ROLE "+quoteIdent(r.User)+" LOGIN PASSWORD "+quoteLiteral(r.Password)+" "+attrs); err != nil {
			return fmt.Errorf("pgxx: provision: alter role %s: %w", r.User, err)
		}
		names = append(names, quoteIdent(r.User))
	}
	if prefix, ok := strings.CutSuffix(owner.User, "_migrator"); ok {
		for _, sibling := range []string{prefix + "_app", prefix + "_system"} {
			if slices.ContainsFunc(roles, func(c *pgx.ConnConfig) bool { return c.User == sibling }) {
				continue
			}
			attrs := "NOLOGIN NOBYPASSRLS"
			if strings.HasSuffix(sibling, "_system") {
				attrs = "NOLOGIN BYPASSRLS"
			}
			if _, err := admin.Exec(ctx, "DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = "+quoteLiteral(sibling)+") THEN CREATE ROLE "+
				quoteIdent(sibling)+" "+attrs+"; END IF; END $$"); err != nil {
				return fmt.Errorf("pgxx: provision: create role %s: %w", sibling, err)
			}
			names = append(names, quoteIdent(sibling))
		}
	}

	db := quoteIdent(owner.Database)
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_database WHERE datname = $1)", owner.Database).Scan(&exists); err != nil {
		return fmt.Errorf("pgxx: provision: database %s: %w", owner.Database, err)
	}
	if !exists {
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+db+" OWNER "+quoteIdent(owner.User)); err != nil {
			return fmt.Errorf("pgxx: provision: create database %s: %w", owner.Database, err)
		}
	}
	for _, sql := range []string{"REVOKE ALL ON DATABASE " + db + " FROM PUBLIC", "GRANT CONNECT ON DATABASE " + db + " TO " + strings.Join(names, ", ")} {
		if _, err := admin.Exec(ctx, sql); err != nil {
			return fmt.Errorf("pgxx: provision: %s: %w", sql, err)
		}
	}

	// The public schema belongs to the migrator; the other roles may only use it (the migrations grant them the tables).
	inDB := admin.Config().Copy()
	inDB.Database = owner.Database
	conn, err := pgx.ConnectConfig(ctx, inDB)
	if err != nil {
		return fmt.Errorf("pgxx: provision: connect %s: %w", owner.Database, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	stmts := []string{"REVOKE CREATE ON SCHEMA public FROM PUBLIC", "ALTER SCHEMA public OWNER TO " + quoteIdent(owner.User)}
	if len(names) > 1 {
		stmts = append(stmts, "GRANT USAGE ON SCHEMA public TO "+strings.Join(names[1:], ", "))
	}
	for _, sql := range stmts {
		if _, err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("pgxx: provision: %s: %w", sql, err)
		}
	}
	return nil
}

func quoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
