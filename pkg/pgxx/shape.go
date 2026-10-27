package pgxx

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// SchemaShape describes a schema's tables, indexes, policies, triggers, privileges and functions as sorted lines that do not depend on the schema's name, so that
// two schemas built by different migration sets can be compared. A service's tenant migration set (TenantSchemas) is written beside its public migrations by
// hand; a test per service applies both and requires the shapes of public and of a tenant schema to be equal, so the two sets cannot drift apart. Function
// bodies are not compared (the tenant set rewrites them on purpose to bind the schema they live in); their signatures, security mode and settings are.
//
// skip names tables (and their indexes, policies, triggers and privileges) to leave out: the tables of the public schema that are not tenant data.
func SchemaShape(ctx context.Context, q Querier, schema string, skip ...string) ([]string, error) {
	var lines []string
	add := func(format string, args ...any) { lines = append(lines, norm(fmt.Sprintf(format, args...), schema)) }
	collect := func(sql string, each func(r pgx.Rows) error, args ...any) error {
		rows, err := q.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := each(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	skipped := func(table string) bool { return slices.Contains(skip, table) }

	// Tables (partition children are not described: their parent is) and their columns.
	err := collect(`
		SELECT c.relname, c.relkind::text, COALESCE(pg_get_partkeydef(c.oid), ''), c.relrowsecurity, c.relforcerowsecurity
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition`, func(r pgx.Rows) error {
		var name, kind, part string
		var rls, force bool
		if err := r.Scan(&name, &kind, &part, &rls, &force); err != nil {
			return err
		}
		if !skipped(name) {
			add("table %s kind=%s partition=%s rls=%t force=%t", name, kind, part, rls, force)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, err
	}
	err = collect(`
		SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull, COALESCE(pg_get_expr(d.adbin, d.adrelid), ''), a.attidentity::text, a.attgenerated::text
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition AND a.attnum > 0 AND NOT a.attisdropped`, func(r pgx.Rows) error {
		var table, col, typ, def, ident, gen string
		var notNull bool
		if err := r.Scan(&table, &col, &typ, &notNull, &def, &ident, &gen); err != nil {
			return err
		}
		if !skipped(table) {
			add("column %s.%s %s notnull=%t default=%s identity=%s generated=%s", table, col, typ, notNull, def, ident, gen)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, err
	}

	// Constraints, indexes, policies, triggers.
	err = collect(`
		SELECT c.relname, k.conname, pg_get_constraintdef(k.oid)
		FROM pg_constraint k JOIN pg_class c ON c.oid = k.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND NOT c.relispartition AND k.contype <> 'n'`, func(r pgx.Rows) error {
		var table, name, def string
		if err := r.Scan(&table, &name, &def); err != nil {
			return err
		}
		if !skipped(table) {
			add("constraint %s %s %s", table, name, def)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, err
	}
	err = collect(`
		SELECT t.relname, i.relname, pg_get_indexdef(x.indexrelid)
		FROM pg_index x JOIN pg_class i ON i.oid = x.indexrelid JOIN pg_class t ON t.oid = x.indrelid JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = $1 AND NOT t.relispartition AND NOT i.relispartition`, func(r pgx.Rows) error {
		var table, name, def string
		if err := r.Scan(&table, &name, &def); err != nil {
			return err
		}
		if !skipped(table) {
			add("index %s %s %s", table, name, def)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, err
	}
	err = collect(`
		SELECT tablename, policyname, permissive, array_to_string(roles, ','), cmd, COALESCE(qual, ''), COALESCE(with_check, '')
		FROM pg_policies WHERE schemaname = $1`, func(r pgx.Rows) error {
		var table, name, perm, roles, cmd, qual, check string
		if err := r.Scan(&table, &name, &perm, &roles, &cmd, &qual, &check); err != nil {
			return err
		}
		if !skipped(table) {
			add("policy %s %s %s roles=%s cmd=%s using=%s check=%s", table, name, perm, roles, cmd, qual, check)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, err
	}
	err = collect(`
		SELECT c.relname, g.tgname, pg_get_triggerdef(g.oid)
		FROM pg_trigger g JOIN pg_class c ON c.oid = g.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND NOT g.tgisinternal AND NOT c.relispartition`, func(r pgx.Rows) error {
		var table, name, def string
		if err := r.Scan(&table, &name, &def); err != nil {
			return err
		}
		if !skipped(table) {
			add("trigger %s %s %s", table, name, def)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, err
	}

	// Privileges on tables and columns, by grantee.
	err = collect(`
		SELECT c.relname, COALESCE(NULLIF(a.grantee, 0)::regrole::text, 'public'), array_to_string(array_agg(a.privilege_type ORDER BY a.privilege_type), ',')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace, aclexplode(c.relacl) a
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition AND a.grantee <> c.relowner
		GROUP BY 1, 2`, func(r pgx.Rows) error {
		var table, role, privs string
		if err := r.Scan(&table, &role, &privs); err != nil {
			return err
		}
		if !skipped(table) {
			add("grant %s %s %s", table, role, privs)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, err
	}
	err = collect(`
		SELECT c.relname, t.attname, COALESCE(NULLIF(a.grantee, 0)::regrole::text, 'public'), array_to_string(array_agg(a.privilege_type ORDER BY a.privilege_type), ',')
		FROM pg_attribute t JOIN pg_class c ON c.oid = t.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace, aclexplode(t.attacl) a
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition AND NOT t.attisdropped AND a.grantee <> c.relowner
		GROUP BY 1, 2, 3`, func(r pgx.Rows) error {
		var table, col, role, privs string
		if err := r.Scan(&table, &col, &role, &privs); err != nil {
			return err
		}
		if !skipped(table) {
			add("grant %s.%s %s %s", table, col, role, privs)
		}
		return nil
	}, schema)
	if err != nil {
		return nil, err
	}

	// Functions: signature, result, security mode, settings, and who may execute.
	err = collect(`
		SELECT p.proname, pg_get_function_identity_arguments(p.oid), pg_get_function_result(p.oid), p.prosecdef, p.provolatile::text,
		       COALESCE(array_to_string(p.proconfig, ';'), ''),
		       COALESCE((SELECT array_to_string(array_agg(COALESCE(NULLIF(x.grantee, 0)::regrole::text, 'public') ORDER BY 1), ',')
		                 FROM aclexplode(COALESCE(p.proacl, acldefault('f', p.proowner))) x WHERE x.grantee <> p.proowner), '')
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = $1 AND p.prokind IN ('f', 'p')`, func(r pgx.Rows) error {
		var name, args, result, vol, config, execs string
		var definer bool
		if err := r.Scan(&name, &args, &result, &definer, &vol, &config, &execs); err != nil {
			return err
		}
		add("function %s(%s) returns %s definer=%t volatility=%s config=[%s] execute=[%s]", name, args, result, definer, vol, config, execs)
		return nil
	}, schema)
	if err != nil {
		return nil, err
	}

	slices.Sort(lines)
	return lines, nil
}

// norm removes the schema qualifier from a description: the same shape in two schemas reads the same. A search_path setting that names the schema is
// replaced by a marker, since "bind the schema I live in" is what both sets mean by it.
func norm(s, schema string) string {
	for _, q := range []string{schema, "public"} {
		s = strings.ReplaceAll(s, quoteIdent(q)+".", "")
		s = strings.ReplaceAll(s, q+".", "")
	}
	s = strings.ReplaceAll(s, "search_path="+schema, "search_path=<own>")
	s = strings.ReplaceAll(s, "search_path="+quoteIdent(schema), "search_path=<own>")
	s = strings.ReplaceAll(s, "search_path=public", "search_path=<own>")
	return s
}

// Diff lists the lines only in a, prefixed "-", and only in b, prefixed "+", of two sorted shapes; empty means equal.
func Diff(a, b []string) []string {
	var out []string
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case j == len(b) || (i < len(a) && a[i] < b[j]):
			out = append(out, "- "+a[i])
			i++
		case i == len(a) || b[j] < a[i]:
			out = append(out, "+ "+b[j])
			j++
		default:
			i++
			j++
		}
	}
	return out
}
