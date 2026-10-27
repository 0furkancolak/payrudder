package pgxx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// Moving a tenant's data (ADR 0018), the part that touches one service's database on each side. The order of a whole move, across services and with the directory,
// is pkg/cells.MoveTenant; these are its steps:
//
//	Fence        the source schema stops answering the service's roles, and in-flight transactions are waited for
//	Copy         the destination is built (ProvisionTenant), emptied, filled with every row, and checked against the source, in one transaction
//	RetireSource after the directory points at the destination: the source schema is set aside (cell) or the tenant's rows deleted (pooled)
//	Unfence, DiscardDestination  undo the first two when the move is given up
//
// A side is the pooled database or a cell. The pooled database holds many tenants in shared tables, so rows are picked by tenant_id and the shared tables of the
// pooled side are never emptied; a cell side is a schema that only this tenant owns.

// Endpoint is one place a tenant's data of a service can be.
type Endpoint struct {
	// Cell names the cell; "" is the pooled database.
	Cell string
	// MigratorDSN is the owner role of the service's database there: moving needs DDL (schemas, partitions) and sees every row.
	MigratorDSN string
}

// Pooled reports whether e is the pooled database.
func (e Endpoint) Pooled() bool { return e.Cell == "" }

func (e Endpoint) String() string {
	if e.Pooled() {
		return "pooled"
	}
	return "cell " + e.Cell
}

// Move is the move of one tenant's data of one service. One side must be a cell: a move between two places of the pooled database does not exist.
type Move struct {
	TenantID uuid.UUID
	From, To Endpoint
	Schemas  TenantSchemas
	Log      *slog.Logger
	// FenceWait bounds how long Fence waits for in-flight transactions of the source schema (default 30s).
	FenceWait time.Duration
}

func (m Move) check() error {
	if m.TenantID == uuid.Nil {
		return errors.New("pgxx: move of the nil tenant")
	}
	if m.From.Pooled() && m.To.Pooled() {
		return errors.New("pgxx: a move needs a cell on one side")
	}
	if m.From == m.To {
		return errors.New("pgxx: a move to where the tenant already is")
	}
	if m.From.MigratorDSN == "" || m.To.MigratorDSN == "" {
		return errors.New("pgxx: a move needs the migrator DSN of both sides")
	}
	return m.Schemas.check()
}

func (m Move) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// schema is the tenant's schema in a cell.
func (m Move) schema() string { return tenantdir.Schema(m.TenantID) }

// side is the schema a side's rows are in.
func (m Move) side(e Endpoint) string {
	if e.Pooled() {
		return "public"
	}
	return m.schema()
}

// Fence takes the service's roles' access to the source schema away and waits for every transaction already in it. From then on a writer that still believes the
// tenant lives there (a cached answer of the directory) fails with a permission error instead of writing to a place that is being emptied. The pooled database has
// no schema to close: its tenants are fenced by the directory alone, and a process whose answer is older than the directory's TTL can still write there, which is
// why the move waits that long first (pkg/cells).
func (m Move) Fence(ctx context.Context) error {
	if err := m.check(); err != nil {
		return err
	}
	if m.From.Pooled() {
		return nil
	}
	conn, err := pgx.Connect(ctx, m.From.MigratorDSN)
	if err != nil {
		return fmt.Errorf("pgxx: connect %s: %w", m.From, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	for _, role := range m.Schemas.Roles {
		if _, err := conn.Exec(ctx, "REVOKE USAGE ON SCHEMA "+quoteIdent(m.schema())+" FROM "+quoteIdent(role)); err != nil {
			return fmt.Errorf("pgxx: fence %s: %w", m.schema(), err)
		}
	}
	// A role in the middle of a transaction keeps its locks until it ends; asking for the strongest lock on every table waits for them all.
	wait := m.FenceWait
	if wait <= 0 {
		wait = 30 * time.Second
	}
	tables, err := schemaTables(ctx, conn, m.schema())
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", wait.Milliseconds())); err != nil {
		return err
	}
	quoted := make([]string, len(tables))
	for i, t := range tables {
		quoted[i] = qualified(m.schema(), t.name)
	}
	if _, err := tx.Exec(ctx, "LOCK TABLE "+strings.Join(quoted, ", ")+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		return fmt.Errorf("pgxx: wait for transactions in %s: %w", m.schema(), err)
	}
	return tx.Commit(ctx)
}

// Unfence gives the roles their access to the source schema back (a move given up).
func (m Move) Unfence(ctx context.Context) error {
	if err := m.check(); err != nil {
		return err
	}
	if m.From.Pooled() {
		return nil
	}
	conn, err := pgx.Connect(ctx, m.From.MigratorDSN)
	if err != nil {
		return fmt.Errorf("pgxx: connect %s: %w", m.From, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	for _, role := range m.Schemas.Roles {
		if _, err := conn.Exec(ctx, "GRANT USAGE ON SCHEMA "+quoteIdent(m.schema())+" TO "+quoteIdent(role)); err != nil {
			return fmt.Errorf("pgxx: unfence %s: %w", m.schema(), err)
		}
	}
	return nil
}

// TableCount is what a copy moved for one table.
type TableCount struct {
	Table string
	Rows  int64
}

// CopyReport is the outcome of Copy: the rows of each table, source and destination equal.
type CopyReport struct {
	Tables []TableCount
}

// Rows is the total over the tables.
func (r CopyReport) Rows() int64 {
	var n int64
	for _, t := range r.Tables {
		n += t.Rows
	}
	return n
}

// Copy builds the destination if it is a cell, and fills it with every row of the tenant, in one transaction that commits only when each table's row count and a
// checksum of its rows are the same on both sides. Whatever the destination held of the tenant before is deleted first, so a copy that failed half way, or a move
// started again, begins clean (the destination is not serving: the directory marks the tenant as moving). Sequences of identity columns continue after the
// highest value copied, so ids keep their order. Into the pooled database identity values are not kept (its sequences are shared with other tenants): the rows get
// new ones in the order of the old, which is all an outbox needs, and no table another table points at may have one.
func (m Move) Copy(ctx context.Context) (CopyReport, error) {
	var rep CopyReport
	if err := m.check(); err != nil {
		return rep, err
	}
	if !m.To.Pooled() {
		if _, err := ProvisionTenant(ctx, m.To.MigratorDSN, m.Schemas, m.TenantID, m.Log); err != nil {
			return rep, err
		}
	}
	src, err := pgx.Connect(ctx, m.From.MigratorDSN)
	if err != nil {
		return rep, fmt.Errorf("pgxx: connect %s: %w", m.From, err)
	}
	defer func() { _ = src.Close(context.WithoutCancel(ctx)) }()
	dst, err := pgx.Connect(ctx, m.To.MigratorDSN)
	if err != nil {
		return rep, fmt.Errorf("pgxx: connect %s: %w", m.To, err)
	}
	defer func() { _ = dst.Close(context.WithoutCancel(ctx)) }()
	srcSchema, dstSchema := m.side(m.From), m.side(m.To)

	// The tables are the ones of the cell side; the pooled side must have each of them.
	cellConn := src
	if m.From.Pooled() {
		cellConn = dst
	}
	tables, err := orderedTables(ctx, cellConn, m.schema())
	if err != nil {
		return rep, err
	}

	// Consistent snapshots, and text forms that do not depend on the session, for the checksums.
	srcTx, err := src.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return rep, err
	}
	defer func() { _ = srcTx.Rollback(context.WithoutCancel(ctx)) }()
	dstTx, err := dst.Begin(ctx)
	if err != nil {
		return rep, err
	}
	defer func() { _ = dstTx.Rollback(context.WithoutCancel(ctx)) }()
	for _, tx := range []pgx.Tx{srcTx, dstTx} {
		for _, stmt := range []string{"SET LOCAL TimeZone = 'UTC'", "SET LOCAL DateStyle = 'ISO, YMD'", "SET LOCAL extra_float_digits = 3", "SET LOCAL bytea_output = 'hex'", "SET LOCAL lock_timeout = '30s'"} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return rep, err
			}
		}
		// Every tenant table is under forced row-level security, the owner's reads and inserts included: the tenant is the one the transaction names.
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, m.TenantID.String()); err != nil {
			return rep, err
		}
	}

	if m.To.Pooled() {
		if err := refuseReferencedIdentities(ctx, srcTx, srcSchema, tables); err != nil {
			return rep, err
		}
		for _, t := range slices.Backward(tables) {
			if _, err := dstTx.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1", qualified(dstSchema, t.name)), m.TenantID); err != nil {
				return rep, fmt.Errorf("pgxx: empty %s of the tenant: %w", t.name, err)
			}
		}
	} else {
		names := make([]string, len(tables))
		for i, t := range tables {
			names[i] = qualified(dstSchema, t.name)
		}
		if _, err := dstTx.Exec(ctx, "TRUNCATE "+strings.Join(names, ", ")); err != nil {
			return rep, fmt.Errorf("pgxx: empty %s: %w", dstSchema, err)
		}
	}

	for _, t := range tables {
		cols, err := sameColumns(ctx, srcTx, srcSchema, dstTx, dstSchema, t.name)
		if err != nil {
			return rep, err
		}
		if m.From.Pooled() || m.To.Pooled() {
			if !slices.ContainsFunc(cols, func(c column) bool { return c.name == "tenant_id" }) {
				return rep, fmt.Errorf("pgxx: table %s has no tenant_id: its rows cannot be picked out of the pooled database", t.name)
			}
		}
		if t.partitioned {
			if err := copyPartitions(ctx, srcTx, srcSchema, dstTx, dstSchema, t.name); err != nil {
				return rep, err
			}
		}
		// Identity values: kept between cells, renumbered into the pooled database.
		var keep, order []string
		for _, c := range cols {
			switch {
			case c.identity && m.To.Pooled():
				order = append(order, quoteIdent(c.name))
			default:
				keep = append(keep, quoteIdent(c.name))
			}
		}
		filter := ""
		if m.From.Pooled() {
			filter = fmt.Sprintf(" WHERE tenant_id = '%s'", m.TenantID) // a parsed UUID: nothing to escape
		}
		orderBy := ""
		if len(order) > 0 {
			orderBy = " ORDER BY " + strings.Join(order, ", ")
		}
		list := strings.Join(keep, ", ")
		stage := strings.Join(append(slices.Clone(keep), order...), ", ")
		selectSQL := fmt.Sprintf("SELECT %s FROM %s%s", stage, qualified(srcSchema, t.name), filter)
		if err := copyTable(ctx, srcTx, dstTx, selectSQL, qualified(dstSchema, t.name), list, stage, orderBy); err != nil {
			return rep, fmt.Errorf("pgxx: copy %s: %w", t.name, err)
		}
		dstFilter := ""
		if m.To.Pooled() {
			dstFilter = fmt.Sprintf(" WHERE tenant_id = '%s'", m.TenantID)
		}
		a, err := fingerprint(ctx, srcTx, qualified(srcSchema, t.name), list, filter)
		if err != nil {
			return rep, err
		}
		b, err := fingerprint(ctx, dstTx, qualified(dstSchema, t.name), list, dstFilter)
		if err != nil {
			return rep, err
		}
		if a != b {
			return rep, fmt.Errorf("pgxx: table %s differs after the copy: source %d rows (%s), destination %d rows (%s)", t.name, a.rows, a.sum, b.rows, b.sum)
		}
		rep.Tables = append(rep.Tables, TableCount{Table: t.name, Rows: a.rows})
	}
	if !m.To.Pooled() {
		if err := fixIdentities(ctx, dstTx, dstSchema); err != nil {
			return rep, err
		}
	}
	if err := dstTx.Commit(ctx); err != nil {
		return rep, fmt.Errorf("pgxx: commit the copy: %w", err)
	}
	m.log().InfoContext(ctx, "tenant data copied", "tenant_id", m.TenantID, "from", m.From.String(), "to", m.To.String(), "tables", len(rep.Tables), "rows", rep.Rows())
	return rep, nil
}

// RetireSource is the last step, once the directory points at the destination. A cell source is set aside under a name that is no tenant schema
// (m_<id>_<unix>) with its access still closed, so a wrong move can be undone by hand and DropRetired removes it later; the pooled source's rows of the tenant
// are deleted. It returns the new name of the schema, or "" for the pooled source.
func (m Move) RetireSource(ctx context.Context, now time.Time) (string, error) {
	if err := m.check(); err != nil {
		return "", err
	}
	conn, err := pgx.Connect(ctx, m.From.MigratorDSN)
	if err != nil {
		return "", fmt.Errorf("pgxx: connect %s: %w", m.From, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if !m.From.Pooled() {
		retired := fmt.Sprintf("m_%s_%d", strings.TrimPrefix(m.schema(), "t_"), now.Unix())
		if _, err := conn.Exec(ctx, "ALTER SCHEMA "+quoteIdent(m.schema())+" RENAME TO "+quoteIdent(retired)); err != nil {
			return "", fmt.Errorf("pgxx: set %s aside: %w", m.schema(), err)
		}
		return retired, nil
	}
	return "", m.deletePooledRows(ctx, conn, m.To)
}

// deletePooledRows deletes the tenant's rows from the pooled database conn is connected to; the tables are the ones of the cell on the other side of the move.
func (m Move) deletePooledRows(ctx context.Context, pooled *pgx.Conn, cell Endpoint) error {
	cellConn, err := pgx.Connect(ctx, cell.MigratorDSN)
	if err != nil {
		return fmt.Errorf("pgxx: connect %s: %w", cell, err)
	}
	tables, err := orderedTables(ctx, cellConn, m.schema())
	_ = cellConn.Close(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	tx, err := pooled.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, m.TenantID.String()); err != nil {
		return err
	}
	for _, t := range slices.Backward(tables) {
		if _, err := tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1", qualified("public", t.name)), m.TenantID); err != nil {
			return fmt.Errorf("pgxx: delete the tenant's rows of %s from the pooled database: %w", t.name, err)
		}
	}
	return tx.Commit(ctx)
}

// DiscardDestination removes what Copy built or filled at the destination, for a move given up before the directory pointed there: the cell's schema, or the
// tenant's rows in the pooled database.
func (m Move) DiscardDestination(ctx context.Context) error {
	if err := m.check(); err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, m.To.MigratorDSN)
	if err != nil {
		return fmt.Errorf("pgxx: connect %s: %w", m.To, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if !m.To.Pooled() {
		_, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+quoteIdent(m.schema())+" CASCADE")
		return err
	}
	return m.deletePooledRows(ctx, conn, m.From)
}

// RetiredSchema is a source schema set aside by a move.
type RetiredSchema struct {
	Name     string
	TenantID uuid.UUID
	At       time.Time
}

var retiredName = regexp.MustCompile(`^m_([0-9a-f]{32})_([0-9]+)$`)

// RetiredSchemas lists the schemas RetireSource set aside in the database q is connected to.
func RetiredSchemas(ctx context.Context, q Querier) ([]RetiredSchema, error) {
	rows, err := q.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname ~ '^m_[0-9a-f]{32}_[0-9]+$' ORDER BY nspname`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	var out []RetiredSchema
	for _, n := range names {
		m := retiredName.FindStringSubmatch(n)
		id, err := uuid.Parse(m[1])
		if err != nil {
			continue
		}
		var secs int64
		if _, err := fmt.Sscan(m[2], &secs); err != nil {
			continue
		}
		out = append(out, RetiredSchema{Name: n, TenantID: id, At: time.Unix(secs, 0).UTC()})
	}
	return out, nil
}

// DropRetired removes a schema set aside by RetireSource. The name must be one RetiredSchemas lists.
func DropRetired(ctx context.Context, migratorDSN, name string) error {
	if !retiredName.MatchString(name) {
		return fmt.Errorf("pgxx: %q is not a retired tenant schema", name)
	}
	conn, err := pgx.Connect(ctx, migratorDSN)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	_, err = conn.Exec(ctx, "DROP SCHEMA "+quoteIdent(name)+" CASCADE")
	return err
}
