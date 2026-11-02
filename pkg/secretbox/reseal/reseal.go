// Package reseal moves every box a service has stored to the key that seals now (docs/runbooks/secrets-key-rotation.md, step 3). It walks the columns that hold
// boxes (places.go) in the pooled database and, in cells mode, in every tenant schema of every cell, opens each box that another key sealed with the associated data
// its writer used and seals it again with the active key.
//
// It can be run again at any time: only boxes of another key are picked, so a run that stopped half way, or a second run, finds what is left and nothing else.
// A row that its service replaced while the job ran is left alone (the update names the box it read), and a box that does not open is counted, never changed.
package reseal

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
)

// KeyColumn is a column of a table's primary key and its Postgres type.
type KeyColumn struct{ Name, Type string }

// Column is one column of boxes and what each box is bound to.
type Column struct {
	Table, Column string
	// Key is the primary key, in order: the rows are visited in key order and found again by it.
	Key []KeyColumn
	// Tenant says the table is a tenant's own: it is in the public schema of the pooled database and in the schema of each tenant placed in a cell. Others are
	// in the pooled database only.
	Tenant bool
	// AAD is the associated data the writer sealed the box with, from the text of the key columns.
	AAD func(key []string) []byte
}

func (c Column) name() string { return c.Table + "." + c.Column }

// Report is what a run found in one column, over every place it visited.
type Report struct {
	Column string
	// Boxes are the boxes in the column when the run ended, Current those of them sealed with the active key.
	Boxes, Current int
	// Resealed boxes were sealed again; with a dry run, boxes that would be.
	Resealed int
	// Changed boxes were replaced by their service while the job ran, and left alone.
	Changed int
	// Unreadable boxes did not open (a key this process does not hold, or other associated data than the writer's) and were left as they are.
	Unreadable int
}

// Input is what a run needs.
type Input struct {
	// Box seals with the active key and opens with every key it holds.
	Box     *secretbox.Box
	Columns []Column
	// Pooled is the pool of the system role of the pooled database; Cells those of the cells (by name), if there are any.
	Pooled *pgxpool.Pool
	Cells  map[string]*pgxpool.Pool
	// Batch is the number of boxes read at a time; 0 is 200.
	Batch int
	// DryRun opens every box that would be sealed again and changes nothing.
	DryRun bool
	Log    *slog.Logger
}

// Run visits the pooled database and every tenant schema of every cell and returns one report per column, in the order of in.Columns.
func Run(ctx context.Context, in Input) ([]Report, error) {
	if in.Batch <= 0 {
		in.Batch = 200
	}
	if in.Log == nil {
		in.Log = slog.Default()
	}
	reports := make([]Report, len(in.Columns))
	for i, c := range in.Columns {
		reports[i].Column = c.name()
	}
	visit := func(q querier, cols []int) error {
		for _, i := range cols {
			r, err := in.Columns[i].run(ctx, q, in)
			if err != nil {
				return fmt.Errorf("%s: %w", in.Columns[i].name(), err)
			}
			reports[i].Boxes += r.Boxes
			reports[i].Current += r.Current
			reports[i].Resealed += r.Resealed
			reports[i].Changed += r.Changed
			reports[i].Unreadable += r.Unreadable
		}
		return nil
	}
	all, tenant := make([]int, len(in.Columns)), []int{}
	for i, c := range in.Columns {
		all[i] = i
		if c.Tenant {
			tenant = append(tenant, i)
		}
	}
	if err := visit(in.Pooled, all); err != nil {
		return reports, fmt.Errorf("the pooled database: %w", err)
	}
	cellNames := make([]string, 0, len(in.Cells))
	for n := range in.Cells {
		cellNames = append(cellNames, n)
	}
	slices.Sort(cellNames)
	for _, name := range cellNames {
		ids, err := pgxx.TenantSchemaIDs(ctx, in.Cells[name])
		if err != nil {
			return reports, fmt.Errorf("cell %s: %w", name, err)
		}
		in.Log.InfoContext(ctx, "cell", "cell", name, "tenant_schemas", len(ids))
		for _, id := range ids {
			if err := pgxx.InTenantSchemaTx(ctx, in.Cells[name], id, func(tx pgx.Tx) error { return visit(tx, tenant) }); err != nil {
				return reports, fmt.Errorf("cell %s, tenant %s: %w", name, id, err)
			}
		}
	}
	return reports, nil
}

// querier is a pool or a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type boxRow struct {
	key []string
	box []byte
}

// run re-seals one column in the place q looks at.
func (c Column) run(ctx context.Context, q querier, in Input) (Report, error) {
	var r Report
	active := []byte{in.Box.ActiveID()}
	names, texts, equal, typed := make([]string, len(c.Key)), make([]string, len(c.Key)), make([]string, len(c.Key)), make([]string, len(c.Key))
	for i, k := range c.Key {
		names[i], texts[i], typed[i] = k.Name, k.Name+"::text", fmt.Sprintf("$%d::%s", i+2, k.Type)
		equal[i] = k.Name + " = " + typed[i]
	}
	// The statements are built from the tables in places.go, never from input. A box of another key starts with another byte; a NULL box is not a box.
	sel := fmt.Sprintf("SELECT %s, %s FROM %s WHERE substring(%s, 1, 1) <> $1", strings.Join(texts, ", "), c.Column, c.Table, c.Column)
	next := fmt.Sprintf(" AND (%s) > (%s)", strings.Join(names, ", "), strings.Join(typed, ", "))
	order := fmt.Sprintf(" ORDER BY %s LIMIT %d", strings.Join(names, ", "), in.Batch)
	// The box is named in the update: a row its service replaced since it was read is not touched.
	update := fmt.Sprintf("UPDATE %s SET %s = $1 WHERE %s AND %s = $%d", c.Table, c.Column, strings.Join(equal, " AND "), c.Column, len(c.Key)+2)

	var last []any
	for {
		sql, args := sel+order, []any{active}
		if last != nil {
			sql, args = sel+next+order, append(args, last...)
		}
		rows, err := q.Query(ctx, sql, args...)
		if err != nil {
			return r, err
		}
		batch, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (boxRow, error) {
			b := boxRow{key: make([]string, len(c.Key))}
			dest := make([]any, 0, len(c.Key)+1)
			for i := range b.key {
				dest = append(dest, &b.key[i])
			}
			return b, row.Scan(append(dest, &b.box)...)
		})
		if err != nil {
			return r, err
		}
		for _, b := range batch {
			aad := c.AAD(b.key)
			if in.DryRun {
				if _, err := in.Box.Open(b.box, aad); err != nil {
					r.unreadable(ctx, c, b, in.Log)
					continue
				}
				r.Resealed++
				continue
			}
			sealed, err := in.Box.Reseal(b.box, aad)
			if err != nil {
				r.unreadable(ctx, c, b, in.Log)
				continue
			}
			tag, err := q.Exec(ctx, update, append(append([]any{sealed}, keyArgs(b.key)...), b.box)...)
			if err != nil {
				return r, err
			}
			if tag.RowsAffected() == 0 {
				r.Changed++
			} else {
				r.Resealed++
			}
		}
		if len(batch) < in.Batch {
			break
		}
		last = keyArgs(batch[len(batch)-1].key)
	}
	rows, err := q.Query(ctx, fmt.Sprintf("SELECT count(%[1]s), count(%[1]s) FILTER (WHERE substring(%[1]s, 1, 1) = $1) FROM %[2]s", c.Column, c.Table), active)
	if err != nil {
		return r, err
	}
	counts, err := pgx.CollectOneRow(rows, func(row pgx.CollectableRow) ([2]int, error) {
		var n [2]int
		return n, row.Scan(&n[0], &n[1])
	})
	r.Boxes, r.Current = counts[0], counts[1]
	return r, err
}

func (r *Report) unreadable(ctx context.Context, c Column, b boxRow, log *slog.Logger) {
	r.Unreadable++
	log.WarnContext(ctx, "a box does not open: left as it is", "column", c.name(), "key", b.key)
}

func keyArgs(key []string) []any {
	out := make([]any, len(key))
	for i, k := range key {
		out[i] = k
	}
	return out
}
