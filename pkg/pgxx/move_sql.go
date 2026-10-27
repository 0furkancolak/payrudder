package pgxx

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
)

// The statements and small helpers of Move.

type tableInfo struct {
	name        string
	partitioned bool
}

type column struct {
	name     string
	identity bool
}

func qualified(schema, table string) string { return quoteIdent(schema) + "." + quoteIdent(table) }

// schemaTables lists the tables of a schema that hold data: partitions are reached through their parent, and the migration bookkeeping is not data.
func schemaTables(ctx context.Context, q Querier, schema string) ([]tableInfo, error) {
	rows, err := q.Query(ctx, `
		SELECT c.relname, c.relkind = 'p'
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition AND c.relname <> 'goose_db_version'
		ORDER BY c.relname`, schema)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (tableInfo, error) {
		var t tableInfo
		err := r.Scan(&t.name, &t.partitioned)
		return t, err
	})
}

// foreignKeys returns, per table of the schema, the tables of the schema it refers to.
func foreignKeys(ctx context.Context, q Querier, schema string) (map[string][]string, error) {
	rows, err := q.Query(ctx, `
		SELECT c.relname, p.relname
		FROM pg_constraint k
		JOIN pg_class c ON c.oid = k.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_class p ON p.oid = k.confrelid JOIN pg_namespace pn ON pn.oid = p.relnamespace
		WHERE k.contype = 'f' AND n.nspname = $1 AND pn.nspname = $1 AND c.oid <> p.oid AND NOT c.relispartition AND NOT p.relispartition`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		out[child] = append(out[child], parent)
	}
	return out, rows.Err()
}

// orderedTables lists the tables of a schema with the tables a table refers to before it, so rows can be inserted in this order and deleted in the reverse.
func orderedTables(ctx context.Context, q Querier, schema string) ([]tableInfo, error) {
	tables, err := schemaTables(ctx, q, schema)
	if err != nil {
		return nil, err
	}
	fks, err := foreignKeys(ctx, q, schema)
	if err != nil {
		return nil, err
	}
	var out []tableInfo
	done := map[string]bool{}
	for len(out) < len(tables) {
		progressed := false
		for _, t := range tables {
			if done[t.name] || slices.ContainsFunc(fks[t.name], func(p string) bool { return !done[p] }) {
				continue
			}
			out, done[t.name], progressed = append(out, t), true, true
		}
		if !progressed {
			return nil, fmt.Errorf("pgxx: the tables of %s refer to each other in a cycle; they cannot be copied in an order", schema)
		}
	}
	return out, nil
}

// refuseReferencedIdentities fails when a table that another table points at has an identity column: renumbering it (what moving into the pooled database does)
// would break the references.
func refuseReferencedIdentities(ctx context.Context, q Querier, schema string, tables []tableInfo) error {
	fks, err := foreignKeys(ctx, q, schema)
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for _, parents := range fks {
		for _, p := range parents {
			referenced[p] = true
		}
	}
	for _, t := range tables {
		if !referenced[t.name] {
			continue
		}
		cols, err := columnsOf(ctx, q, schema, t.name)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(cols, func(c column) bool { return c.identity }) {
			return fmt.Errorf("pgxx: table %s has an identity column other tables refer to: moving into the pooled database would renumber it", t.name)
		}
	}
	return nil
}

// columnsOf lists the columns that hold data (not generated ones) in table order.
func columnsOf(ctx context.Context, q Querier, schema, table string) ([]column, error) {
	rows, err := q.Query(ctx, `
		SELECT attname, attidentity <> ''
		FROM pg_attribute WHERE attrelid = to_regclass($1) AND attnum > 0 AND NOT attisdropped AND attgenerated = ''
		ORDER BY attnum`, qualified(schema, table))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (column, error) {
		var c column
		err := r.Scan(&c.name, &c.identity)
		return c, err
	})
}

// sameColumns returns the destination's columns of a table, after checking that the source has exactly those: a copy between schemas that differ means a
// migration is pending on one side, and it must not guess.
func sameColumns(ctx context.Context, src Querier, srcSchema string, dst Querier, dstSchema, table string) ([]column, error) {
	a, err := columnsOf(ctx, src, srcSchema, table)
	if err != nil {
		return nil, err
	}
	b, err := columnsOf(ctx, dst, dstSchema, table)
	if err != nil {
		return nil, err
	}
	if len(a) == 0 {
		return nil, fmt.Errorf("pgxx: table %s is not in %s", table, srcSchema)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("pgxx: table %s is not in %s", table, dstSchema)
	}
	names := func(cs []column) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.name
		}
		slices.Sort(out)
		return out
	}
	if !slices.Equal(names(a), names(b)) {
		return nil, fmt.Errorf("pgxx: table %s has columns %v in %s and %v in %s: migrate both sides first", table, names(a), srcSchema, names(b), dstSchema)
	}
	return b, nil
}

// copyPartitions creates in the destination the partitions of a table that the source has and the destination lacks, under the same names and bounds.
func copyPartitions(ctx context.Context, srcTx pgx.Tx, srcSchema string, dstTx pgx.Tx, dstSchema, table string) error {
	rows, err := srcTx.Query(ctx, `
		SELECT c.relname, pg_get_expr(c.relpartbound, c.oid), c.relkind = 'p'
		FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = to_regclass($1) ORDER BY c.relname`, qualified(srcSchema, table))
	if err != nil {
		return err
	}
	type part struct {
		name, bound string
		sub         bool
	}
	parts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (part, error) {
		var p part
		err := r.Scan(&p.name, &p.bound, &p.sub)
		return p, err
	})
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.sub {
			return fmt.Errorf("pgxx: partition %s of %s is itself partitioned: not supported", p.name, table)
		}
		var existing *string
		if err := dstTx.QueryRow(ctx, `SELECT to_regclass($1)::text`, qualified(dstSchema, p.name)).Scan(&existing); err != nil {
			return err
		}
		if existing != nil {
			continue
		}
		if _, err := dstTx.Exec(ctx, fmt.Sprintf("CREATE TABLE %s PARTITION OF %s %s", qualified(dstSchema, p.name), qualified(dstSchema, table), p.bound)); err != nil {
			return fmt.Errorf("pgxx: create partition %s of %s: %w", p.name, table, err)
		}
	}
	return nil
}

type fp struct {
	rows int64
	sum  string
}

// fingerprint is the row count of a table and an order-independent checksum of its rows over the given columns.
func fingerprint(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, table, columns, filter string) (fp, error) {
	var f fp
	err := q.QueryRow(ctx, fmt.Sprintf(`SELECT count(*), COALESCE(sum(hashtextextended(row(%s)::text, 0)::numeric), 0)::text FROM %s%s`, columns, table, filter)).Scan(&f.rows, &f.sum)
	return f, err
}

// copyTable streams the rows of selectSQL (on the source transaction) into the destination table (on the destination transaction) in PostgreSQL's binary COPY
// format, without decoding them. COPY cannot write into a table under row-level security, which every tenant table is, so the rows land in a temporary table
// with the columns of stage (the destination's own columns, and the ones only the order is taken from) and are inserted from there: the insert is checked by the
// policy like any other, in the order of orderBy.
func copyTable(ctx context.Context, srcTx, dstTx pgx.Tx, selectSQL, dstTable, columns, stage, orderBy string) error {
	if _, err := dstTx.Exec(ctx, fmt.Sprintf("CREATE TEMP TABLE _move_stage ON COMMIT DROP AS SELECT %s FROM %s WITH NO DATA", stage, dstTable)); err != nil {
		return err
	}
	pr, pw := io.Pipe()
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		_, err := srcTx.Conn().PgConn().CopyTo(gctx, pw, "COPY ("+selectSQL+") TO STDOUT (FORMAT binary)")
		_ = pw.CloseWithError(err)
		return err
	})
	g.Go(func() error {
		_, err := dstTx.Conn().PgConn().CopyFrom(gctx, pr, fmt.Sprintf("COPY _move_stage (%s) FROM STDIN (FORMAT binary)", stage))
		_ = pr.CloseWithError(err)
		return err
	})
	if err := g.Wait(); err != nil {
		return err
	}
	// OVERRIDING SYSTEM VALUE keeps the ids of an identity column (GENERATED ALWAYS refuses them otherwise); columns leaves out the ones that are renumbered.
	if _, err := dstTx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (%s) OVERRIDING SYSTEM VALUE SELECT %s FROM _move_stage%s", dstTable, columns, columns, orderBy)); err != nil {
		return err
	}
	_, err := dstTx.Exec(ctx, "DROP TABLE _move_stage")
	return err
}

// fixIdentities moves every identity sequence of the schema past the highest value its column holds, so the next row gets a larger id than any copied.
func fixIdentities(ctx context.Context, tx pgx.Tx, schema string) error {
	rows, err := tx.Query(ctx, `
		SELECT c.relname, a.attname
		FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition AND a.attnum > 0 AND NOT a.attisdropped AND a.attidentity <> ''`, schema)
	if err != nil {
		return err
	}
	type col struct{ table, column string }
	cols, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (col, error) {
		var c col
		err := r.Scan(&c.table, &c.column)
		return c, err
	})
	if err != nil {
		return err
	}
	for _, c := range cols {
		q := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence($1, $2), COALESCE(max(%[1]s), 1), max(%[1]s) IS NOT NULL) FROM %[2]s`, quoteIdent(c.column), qualified(schema, c.table))
		var ignored int64
		if err := tx.QueryRow(ctx, q, qualified(schema, c.table), c.column).Scan(&ignored); err != nil {
			return fmt.Errorf("pgxx: continue the sequence of %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}
