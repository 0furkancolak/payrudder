//go:build integration

package itest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/migrations"
)

// A billing tenant moves between the pooled database and a cell without losing a row: every table of its schema (the credit-note trigger, the self-referencing
// payment links, the identity column of the outbox included), and the pooled rows of other tenants stay where they are. inbox_replies has no tenant_id, so a move
// into or out of the pooled database leaves it where it is; between two cells it goes with the rest.
func TestBillingTenantMovesBetweenThePooledDatabaseAndACell(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	pub := testkit.MigratedDB(t, pg, "billing", migrations.FS)
	cell := testkit.CreateCellDB(t, pg, "billing", "c1")
	cell2 := testkit.CreateCellDB(t, pg, "billing", "c2")
	pooled, err := pgxx.NewPool(ctx, pub.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pooled.Close()
	cellPool, err := pgxx.NewPool(ctx, cell.System)
	if err != nil {
		t.Fatal(err)
	}
	defer cellPool.Close()
	cell2Pool, err := pgxx.NewPool(ctx, cell2.System)
	if err != nil {
		t.Fatal(err)
	}
	defer cell2Pool.Close()

	a, b := uuid.New(), uuid.New()
	seedBillingTenant(t, pooled, pub.Migrator, a)
	seedBillingTenant(t, pooled, pub.Migrator, b)
	wantA, wantB := rowSums(t, pooled, "public", a), rowSums(t, pooled, "public", b)
	if len(wantA) < 15 {
		t.Fatalf("the fixture fills %d tables, want it to cover the schema", len(wantA))
	}
	inbox := func(p *pgxpool.Pool, schema string) int64 {
		t.Helper()
		var n int64
		if err := p.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s.inbox_replies`, schema)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := inbox(pooled, "public"); n != 4 {
		t.Fatalf("seeded %d inbox rows, want 4", n)
	}

	move := func(id uuid.UUID, from, to pgxx.Endpoint) pgxx.CopyReport {
		t.Helper()
		m := pgxx.Move{TenantID: id, From: from, To: to, Schemas: tenantSchemas(), Log: testkit.Logger()}
		rep, err := m.Copy(ctx)
		if err != nil {
			t.Fatalf("copy %s to %s: %v", from, to, err)
		}
		if _, err := m.RetireSource(ctx, time.Now()); err != nil {
			t.Fatalf("retire %s: %v", from, err)
		}
		return rep
	}
	pooledAt := pgxx.Endpoint{MigratorDSN: pub.Migrator}
	c1, c2 := pgxx.Endpoint{Cell: "c1", MigratorDSN: cell.Migrator}, pgxx.Endpoint{Cell: "c2", MigratorDSN: cell2.Migrator}

	rep := move(a, pooledAt, c1)
	if rep.Rows() == 0 {
		t.Fatal("nothing was copied")
	}
	equalSums(t, "in the cell", wantA, rowSums(t, cellPool, tenantdir.Schema(a), a))
	if left := rowSums(t, pooled, "public", a); len(left) != 0 {
		t.Errorf("rows of the tenant are left in the pooled database: %v", left)
	}
	equalSums(t, "the other tenant", wantB, rowSums(t, pooled, "public", b))
	if n := inbox(pooled, "public"); n != 4 {
		t.Errorf("the pooled inbox has %d rows after the move, want the 4 it had: it belongs to no tenant", n)
	}
	if n := inbox(cellPool, tenantdir.Schema(a)); n != 0 {
		t.Errorf("the cell inbox has %d rows, want none: a move out of the pooled database leaves it", n)
	}
	// The identity column goes on after the highest id; a credit note still needs an invoice of its series (the trigger reads the table's own schema).
	var next, top int64
	if err := cellPool.QueryRow(ctx, fmt.Sprintf(`SELECT max(id) FROM %s.outbox`, tenantdir.Schema(a))).Scan(&top); err != nil {
		t.Fatal(err)
	}
	if err := cellPool.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s.outbox (tenant_id, topic, partition_key, message_id) VALUES ($1,'t','k',$2) RETURNING id`, tenantdir.Schema(a)), a, uuid.New()).Scan(&next); err != nil || next <= top {
		t.Fatalf("next outbox id = %d (%v), top was %d", next, err, top)
	}
	if _, err := cellPool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s.outbox WHERE id = $1`, tenantdir.Schema(a)), next); err != nil {
		t.Fatal(err)
	}

	// Cell to cell: everything moves, the inbox included.
	if _, err := cellPool.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.inbox_replies (message_id) VALUES ($1)`, tenantdir.Schema(a)), uuid.New()); err != nil {
		t.Fatal(err)
	}
	move(a, c1, c2)
	equalSums(t, "in the second cell", wantA, rowSums(t, cell2Pool, tenantdir.Schema(a), a))
	if n := inbox(cell2Pool, tenantdir.Schema(a)); n != 1 {
		t.Errorf("the second cell's inbox has %d rows, want the one the first cell held", n)
	}

	// And back to the pooled database: the rows are what they were, the other tenant was never touched.
	move(a, c2, pooledAt)
	equalSums(t, "back in the pooled database", wantA, rowSums(t, pooled, "public", a))
	equalSums(t, "the other tenant", wantB, rowSums(t, pooled, "public", b))
	if n := inbox(pooled, "public"); n != 4 {
		t.Errorf("the pooled inbox has %d rows at the end, want 4", n)
	}
}

// seedBillingTenant gives a tenant a row in every kind of table that has its own shape: a regular invoice with lines, a saga with its events, a credit note (its trigger
// reads the invoice), balances and their application, an outbox with an identity column, self-referencing payment links, an expense inbox, and two replies in the inbox
// that belongs to no tenant.
func seedBillingTenant(t *testing.T, p *pgxpool.Pool, migratorDSN string, tid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	// The configuration tables are written by the app role only: the owner writes them here, as the tenant.
	mig, err := pgx.Connect(ctx, migratorDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mig.Close(ctx) }()
	tx, err := mig.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, s := range []struct {
		sql  string
		args []any
	}{
		{`SELECT set_config('app.tenant_id', $1, true)`, []any{tid.String()}},
		{`INSERT INTO expenses (tenant_id, id, vendor, category, amount_minor, currency, incurred_on) VALUES ($1,$2,'Acme','software',500,'USD','2026-10-10')`, []any{tid, uuid.New()}},
		{`INSERT INTO expense_inboxes (tenant_id, scope, token) VALUES ($1,'',$2)`, []any{tid, uuid.NewString()}},
		{`INSERT INTO dunning_settings (tenant_id, retry_hours) VALUES ($1,'{24,72}')`, []any{tid}},
	} {
		if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("seed %.60s: %v", s.sql, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	inv, saga, note, bal, parent, child := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cust := uuid.New()
	for _, s := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, number, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, finalized_at)
			VALUES ($1,$2,$3,'2026-10','REGULAR','INV-2026-000001',$4,'USD',1000,1000,'paid',$5,now())`, []any{tid, inv, cust, uuid.New(), jobSnapshot}},
		{`INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, amount_minor, description) VALUES ($1,$2,1,'BASE_FEE',600,'Base'), ($1,$2,2,'USAGE',400,'Usage')`, []any{tid, inv}},
		{`INSERT INTO invoice_number_seq (tenant_id, year, next_value) VALUES ($1,2026,2)`, []any{tid}},
		{`INSERT INTO invoice_sagas (tenant_id, id, invoice_id, state, version) VALUES ($1,$2,$3,'PAID',3)`, []any{tid, saga, inv}},
		{`INSERT INTO saga_events (tenant_id, saga_id, seq, from_state, to_state, cause) VALUES ($1,$2,1,'OPEN','PAID','reply:PaymentSucceeded')`, []any{tid, saga}},
		{`INSERT INTO credit_note_seq (tenant_id, year, next_value) VALUES ($1,2026,2)`, []any{tid}},
		{`INSERT INTO credit_notes (tenant_id, id, customer_id, invoice_id, number, currency, total_minor) VALUES ($1,$2,$3,$4,'CN-2026-000001','USD',100)`, []any{tid, note, cust, inv}},
		{`INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_credit_note_id, currency) VALUES ($1,$2,$3,100,$4,'USD')`, []any{tid, cust, bal, note}},
		{`INSERT INTO credit_applications (tenant_id, credit_id, invoice_id, amount_minor) VALUES ($1,$2,$3,40)`, []any{tid, bal, inv}},
		{`INSERT INTO outbox (tenant_id, topic, partition_key, payload, message_id) SELECT $1,'billing.commands','k'||g,'\x00',gen_random_uuid() FROM generate_series(1,5) g`, []any{tid}},
		{`INSERT INTO payment_links (tenant_id, id, invoice_id, token_hash, expires_at) VALUES ($1,$2,$3,decode(md5(random()::text) || md5(random()::text), 'hex'), now() + interval '1 day')`, []any{tid, parent, inv}},
		{`INSERT INTO payment_links (tenant_id, id, invoice_id, token_hash, expires_at, parent_id) VALUES ($1,$2,$3,decode(md5(random()::text) || md5(random()::text), 'hex'), now() + interval '1 day', $4)`, []any{tid, child, inv, parent}},
		{`INSERT INTO tenant_event_outbox (tenant_id, event_id, type, data, occurred_at) VALUES ($1,$2,'invoice.paid','{}'::jsonb,now())`, []any{tid, uuid.New()}},
		{`INSERT INTO inbox_replies (message_id) VALUES ($1), ($2)`, []any{uuid.New(), uuid.New()}},
	} {
		if _, err := p.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("seed %.60s: %v", s.sql, err)
		}
	}
}

// rowSums fingerprints the tenant's rows of every table that has a tenant_id: the row count and an order-independent checksum of the rows, but for the outbox, whose
// identity values are renumbered when a tenant moves into the pooled database (its rows are compared without them).
func rowSums(t *testing.T, p *pgxpool.Pool, schema string, tid uuid.UUID) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := p.Query(ctx, `SELECT c.table_name FROM information_schema.columns c JOIN information_schema.tables t USING (table_schema, table_name)
		WHERE c.table_schema = $1 AND c.column_name = 'tenant_id' AND t.table_type = 'BASE TABLE' ORDER BY 1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, name := range names {
		cols := "t"
		if name == "outbox" {
			cols = "t.tenant_id, t.topic, t.partition_key, t.payload, t.headers, t.message_id, t.ingested_at"
		}
		var n int64
		var sum string
		q := fmt.Sprintf(`SELECT count(*), COALESCE(sum(hashtextextended(row(%s)::text, 0)::numeric), 0)::text FROM %s.%s t WHERE t.tenant_id = $1`, cols, pgx.Identifier{schema}.Sanitize(), pgx.Identifier{name}.Sanitize())
		if err := p.QueryRow(ctx, q, tid).Scan(&n, &sum); err != nil {
			t.Fatalf("sum of %s.%s: %v", schema, name, err)
		}
		if n > 0 {
			out[name] = fmt.Sprintf("%d rows, sum %s", n, sum)
		}
	}
	return out
}

func equalSums(t *testing.T, where string, want, got map[string]string) {
	t.Helper()
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: table %s is %q, want %q", where, name, got[name], w)
		}
	}
	for name, g := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: table %s has %q, want none", where, name, g)
		}
	}
}
