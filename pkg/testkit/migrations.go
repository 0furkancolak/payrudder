package testkit

import (
	"io/fs"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
)

// MigrationCycle checks the Down of a service's migrations on a Postgres of its own: Up builds the schema, Down takes all of it away (only goose's table and the
// extensions are left), and Up again builds exactly the same schema, down to the grants, policies, triggers and function bodies. svc is the short name of the service
// (plans, billing, ...): the migrations grant to the roles CreateServiceDB makes for it, so the database has to be that one, which is why the test starts its own Postgres.
func MigrationCycle(t testing.TB, svc string, migrations fs.FS) {
	t.Helper()
	db := CreateServiceDB(t, StartPostgres(t), svc)
	ctx := startCtx(t)
	if err := pgxx.Migrate(ctx, db.Migrator, migrations, nil); err != nil {
		t.Fatalf("testkit: migrate %s up: %v", svc, err)
	}
	first := schemaFingerprint(t, db.Migrator)
	if len(first) < 5 {
		t.Fatalf("testkit: %s has only %d schema objects after Up: %v", svc, len(first), first)
	}
	if err := pgxx.MigrateDown(ctx, db.Migrator, migrations); err != nil {
		t.Fatalf("testkit: migrate %s down: %v", svc, err)
	}
	for _, l := range schemaFingerprint(t, db.Migrator) {
		if !strings.HasPrefix(l, "ext ") {
			t.Errorf("testkit: %s: Down left %s", svc, l)
		}
	}
	if err := pgxx.Migrate(ctx, db.Migrator, migrations, nil); err != nil {
		t.Fatalf("testkit: migrate %s up again: %v", svc, err)
	}
	again := schemaFingerprint(t, db.Migrator)
	for _, l := range first {
		if !slices.Contains(again, l) {
			t.Errorf("testkit: %s: Up, Down and Up again lost %s", svc, l)
		}
	}
	for _, l := range again {
		if !slices.Contains(first, l) {
			t.Errorf("testkit: %s: Up, Down and Up again added %s", svc, l)
		}
	}
}

// schemaFingerprint describes the public schema of the database one object per line: relations with their row-level-security flags and privileges, columns (position,
// type, default, identity, generation, privileges), constraints, indexes, triggers, policies, the functions that no extension owns (with their bodies and privileges)
// and the extensions. goose's own table is left out.
func schemaFingerprint(t testing.TB, dsn string) []string {
	t.Helper()
	ctx := startCtx(t)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("testkit: connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	rows, err := conn.Query(ctx, fingerprintSQL)
	if err != nil {
		t.Fatalf("testkit: fingerprint: %v", err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("testkit: fingerprint: %v", err)
	}
	sort.Strings(lines)
	return lines
}

const fingerprintSQL = `
SELECT 'rel ' || c.relkind::text || ' ' || c.relname || ' rls=' || c.relrowsecurity || '/' || c.relforcerowsecurity || ' acl=' || coalesce(c.relacl::text, '')
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'S', 'v', 'm') AND c.relname NOT LIKE 'goose_db_version%'
UNION ALL
SELECT 'col ' || c.relname || ' #' || a.attnum || ' ' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod) || ' notnull=' || a.attnotnull
  || ' default=' || coalesce(pg_get_expr(d.adbin, d.adrelid), '') || ' identity=' || a.attidentity::text || ' generated=' || a.attgenerated::text || ' acl=' || coalesce(a.attacl::text, '')
FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND a.attnum > 0 AND NOT a.attisdropped AND c.relname NOT LIKE 'goose_db_version%'
UNION ALL
SELECT 'con ' || c.relname || ' ' || o.conname || ' ' || pg_get_constraintdef(o.oid)
FROM pg_constraint o JOIN pg_class c ON c.oid = o.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relname NOT LIKE 'goose_db_version%'
UNION ALL
SELECT 'idx ' || pg_get_indexdef(i.indexrelid)
FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relname NOT LIKE 'goose_db_version%'
UNION ALL
SELECT 'trg ' || pg_get_triggerdef(g.oid)
FROM pg_trigger g JOIN pg_class c ON c.oid = g.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND NOT g.tgisinternal
UNION ALL
SELECT 'pol ' || tablename || ' ' || policyname || ' ' || cmd || ' ' || roles::text || ' using=' || coalesce(qual, '') || ' check=' || coalesce(with_check, '')
FROM pg_policies WHERE schemaname = 'public'
UNION ALL
SELECT 'fn ' || pg_get_functiondef(p.oid) || ' acl=' || coalesce(p.proacl::text, '')
FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = 'public' AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')
UNION ALL
SELECT 'ext ' || extname FROM pg_extension WHERE extname <> 'plpgsql'`
