//go:build integration

package itest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	billingreset "github.com/0furkancolak/payrudder/services/billing-svc/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/billing-svc/migrations"
)

// triggerRefusal says whether err is the refusal of one of the immutability triggers (migration 00003) with the given hint.
func triggerRefusal(err error, hint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == pgxx.CodeRaiseException && pe.Hint == hint
}

// errRollBack ends a transaction of a test that only looked.
var errRollBack = errors.New("roll back")

// testFinancialRowsAreImmutable: whatever role writes, an invoice that has left draft keeps its money, number, customer, period and snapshot, and the rows of the
// financial tables are not deleted: not by billing_system (the role that may), not by the request path, and not by a reset unless the transaction names the tenant as
// a sandbox's. The grants say the same for the request path and for updates of history.
func testFinancialRowsAreImmutable(t *testing.T) {
	infra(t)
	ctx := context.Background()
	live, customer := uuid.New(), uuid.New()
	when := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

	// An open invoice: nothing about its amounts or identity changes, whoever asks.
	open := seedOne(t, live, customer, "cus_i", "open", "USD", "INV-2026-000501", 1000, "live")
	for name, set := range map[string]string{
		"subtotal": `subtotal_minor = 1`, "total": `total_minor = 1`, "currency": `currency = 'EUR'`, "customer": `customer_id = gen_random_uuid()`, "period": `period_id = '2030-01'`,
		"number": `number = 'INV-2026-009999'`, "snapshot": `snapshot = snapshot || '{"customer_name":"Mallory"}'`,
		"environment": `snapshot = jsonb_set(snapshot, '{environment}', '"sandbox"')`, "finalized_at": `finalized_at = now()`, "back to draft": `status = 'draft'`,
	} {
		// set is one of the constants above, never input.
		_, err := shared.sql.Exec(ctx, `UPDATE invoices SET `+set+` WHERE tenant_id = $1 AND id = $2`, live, open)
		if !triggerRefusal(err, "invoice_frozen") {
			t.Errorf("open invoice, %s: %v, want the invoice_frozen refusal", name, err)
		}
	}
	// What still changes on it: the saga's status, the PDF, the payment method the customer chose, the promotion it used.
	for name, set := range map[string]string{
		"pdf": `pdf_object_key = 'acme/inv.pdf'`, "payment method": `snapshot = jsonb_set(snapshot, '{payment_method}', '"pm_x"')`,
		"promotion": `coupon_code = 'C', promotion_target = 'payment', bonus_credits = 5`, "paid": `status = 'paid'`, "void": `status = 'void'`,
	} {
		if _, err := shared.sql.Exec(ctx, `UPDATE invoices SET `+set+` WHERE tenant_id = $1 AND id = $2`, live, open); err != nil {
			t.Errorf("open invoice, %s: %v", name, err)
		}
	}
	// A draft is still being made: totals, snapshot, and then number, status and finalized_at when it is opened.
	draft := seedOne(t, live, uuid.New(), "cus_d", "draft", "USD", "", 700, "live")
	for name, stmt := range map[string]string{
		"totals":  `UPDATE invoices SET subtotal_minor = 600, total_minor = 650 WHERE tenant_id = $1 AND id = $2`,
		"opening": `UPDATE invoices SET number = 'INV-2026-000502', status = 'open', finalized_at = now() WHERE tenant_id = $1 AND id = $2`,
	} {
		if _, err := shared.sql.Exec(ctx, stmt, live, draft); err != nil {
			t.Errorf("draft, %s: %v", name, err)
		}
	}
	if _, err := shared.sql.Exec(ctx, `UPDATE invoices SET total_minor = 1 WHERE tenant_id = $1 AND id = $2`, live, draft); !triggerRefusal(err, "invoice_frozen") {
		t.Errorf("the invoice that was a draft a moment ago: %v, want the invoice_frozen refusal", err)
	}

	// Rows of every table the deletes are refused on, for a live tenant.
	paid := seedPaidInvoiceIn(t, live, "live", "USD", 1000, 100, when, time.Time{})
	seedCreditNote(t, live, paid, "USD", 100, when)
	lot, credit := uuid.New(), uuid.New()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO invoice_payments (tenant_id, id, invoice_id, method, amount_minor, currency, received_at, recorded_by) VALUES ($1,$2,$3,'cash',100,'USD',now(),'test')`, []any{live, uuid.New(), paid}},
		{`INSERT INTO credit_lots (tenant_id, id, customer_id, granted, remaining, source) VALUES ($1,$2,$3,10,10,'manual')`, []any{live, lot, customer}},
		{`INSERT INTO credit_ledger (tenant_id, id, customer_id, lot_id, kind, amount) VALUES ($1,$2,$3,$4,'grant',10)`, []any{live, uuid.New(), customer, lot}},
		{`INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_adjustment_id, currency) VALUES ($1,$2,$3,10,$4,'USD')`, []any{live, customer, credit, uuid.New()}},
		{`INSERT INTO credit_applications (tenant_id, credit_id, invoice_id, amount_minor) VALUES ($1,$2,$3,10)`, []any{live, credit, paid}},
	} {
		if _, err := shared.sql.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	remaining := func(table string) int {
		t.Helper()
		var n int
		// table is one of the constants below, never input.
		if err := shared.sql.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, live).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	tables := []string{"invoice_lines", "credit_notes", "invoice_payments", "credit_ledger", "credit_applications", "saga_events", "invoices"}
	for _, table := range tables {
		before := remaining(table)
		if before == 0 {
			t.Fatalf("the fixture has no rows of %s: the test would not look at its trigger", table)
		}
		_, err := shared.sql.Exec(ctx, `DELETE FROM `+table+` WHERE tenant_id = $1`, live)
		if !triggerRefusal(err, "financial_row_delete") {
			t.Errorf("DELETE FROM %s of a live tenant: %v, want the financial_row_delete refusal", table, err)
		}
		if got := remaining(table); got != before {
			t.Errorf("%s: %d rows, want %d: the refused delete still deleted", table, got, before)
		}
	}
	// The one delete of live data the product does: a voided invoice gives back the credit it consumed.
	if _, err := shared.sql.Exec(ctx, `UPDATE invoices SET status = 'void' WHERE tenant_id = $1 AND id = $2`, live, paid); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.sql.Exec(ctx, `DELETE FROM credit_applications WHERE tenant_id = $1 AND invoice_id = $2`, live, paid); err != nil {
		t.Errorf("the applications of a voided invoice: %v", err)
	}

	// A transaction that names the tenant as being moved (pkg/pgxx Move) may delete its rows. A sandbox reset may too, but not an invoice of the live environment.
	for name, c := range map[string]struct {
		setting, table string
		ok             bool
	}{
		"a move deletes the ledger":                            {"payrudder.tenant_move", "credit_ledger", true},
		"a move deletes the saga events":                       {"payrudder.tenant_move", "saga_events", true},
		"a reset deletes the lines":                            {"payrudder.sandbox_reset", "invoice_lines", true},
		"a reset of a tenant with a live invoice deletes none": {"payrudder.sandbox_reset", "invoices", false},
	} {
		err := shared.system.InTx(ctx, live, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, c.setting, live.String()); err != nil {
				return err
			}
			// table is one of the constants above, never input.
			if _, err := tx.Exec(ctx, `DELETE FROM `+c.table+` WHERE tenant_id = $1`, live); err != nil {
				return err
			}
			return errRollBack
		})
		if c.ok && !errors.Is(err, errRollBack) || !c.ok && !triggerRefusal(err, "financial_row_delete") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The setting names a tenant: another tenant's name is no leave to delete.
	err := shared.system.InTx(ctx, live, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('payrudder.tenant_move', $1, true), set_config('payrudder.sandbox_reset', $1, true)`, uuid.NewString()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM credit_ledger WHERE tenant_id = $1`, live)
		return err
	})
	if !triggerRefusal(err, "financial_row_delete") {
		t.Errorf("a delete that names another tenant: %v, want the financial_row_delete refusal", err)
	}

	// A reset of a tenant that has a live invoice is refused before it deletes anything.
	_, err = billingreset.New(shared.system).Reset(ctx, live)
	if errkey.Of(err) != "admin.sandbox.live_refused" {
		t.Errorf("a reset of a live tenant: %v, want admin.sandbox.live_refused", err)
	}
	for _, table := range tables {
		if table != "credit_applications" && remaining(table) == 0 { // the applications of the voided invoice went above
			t.Errorf("%s of the live tenant is gone after a refused reset", table)
		}
	}

	// The request path reads history and writes none of it; billing_system cannot update it.
	for _, stmt := range []string{
		`UPDATE invoices SET total_minor = 1 WHERE tenant_id = $1`, `DELETE FROM invoices WHERE tenant_id = $1`, `UPDATE credit_ledger SET amount = 1 WHERE tenant_id = $1`,
		`DELETE FROM credit_ledger WHERE tenant_id = $1`, `DELETE FROM saga_events WHERE tenant_id = $1`, `DELETE FROM credit_notes WHERE tenant_id = $1`,
	} {
		err := pgxx.InTenantTx(ctx, shared.app, live, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt, live)
			return err
		})
		if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
			t.Errorf("billing_app: %s: %v, want permission denied", stmt, err)
		}
	}
	for _, stmt := range []string{
		`UPDATE credit_ledger SET amount = 1 WHERE tenant_id = $1`, `UPDATE saga_events SET cause = 'x' WHERE tenant_id = $1`, `UPDATE invoice_lines SET amount_minor = 1 WHERE tenant_id = $1`,
		`UPDATE invoice_payments SET amount_minor = 1 WHERE tenant_id = $1`, `UPDATE credit_notes SET total_minor = 1 WHERE tenant_id = $1`,
		`UPDATE credit_applications SET amount_minor = 1 WHERE tenant_id = $1`,
	} {
		if _, err := shared.sql.Exec(ctx, stmt, live); pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
			t.Errorf("billing_system: %s: %v, want permission denied", stmt, err)
		}
	}
}

// testCreditLedgerKeyIsUsedOncePerLot: a consume that takes from two lots writes two rows under its key, but one lot never has the key twice, and rows without a key
// are not constrained (migration 00004).
func testCreditLedgerKeyIsUsedOncePerLot(t *testing.T) {
	infra(t)
	ctx := context.Background()
	tid, customer, lot1, lot2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, lot := range []uuid.UUID{lot1, lot2} {
		if _, err := shared.sql.Exec(ctx, `INSERT INTO credit_lots (tenant_id, id, customer_id, granted, remaining, source) VALUES ($1,$2,$3,10,10,'manual')`, tid, lot, customer); err != nil {
			t.Fatal(err)
		}
	}
	write := func(lot uuid.UUID, key *string) error {
		_, err := shared.sql.Exec(ctx, `INSERT INTO credit_ledger (tenant_id, id, customer_id, lot_id, kind, amount, idempotency_key) VALUES ($1,$2,$3,$4,'consume',-1,$5)`,
			tid, uuid.New(), customer, lot, key)
		return err
	}
	key := "consume-1"
	if err := write(lot1, &key); err != nil {
		t.Fatal(err)
	}
	if err := write(lot2, &key); err != nil {
		t.Errorf("the same key on a second lot: %v", err)
	}
	if err := write(lot1, &key); pgxx.ErrCode(err) != pgxx.CodeUniqueViolation {
		t.Errorf("the same key on the same lot again: %v, want a unique violation", err)
	}
	if err := write(lot1, nil); err != nil {
		t.Errorf("a row without a key: %v", err)
	}
	if err := write(lot1, nil); err != nil {
		t.Errorf("a second row without a key: %v", err)
	}
}

// testRepeatedLedgerKeysAreRenamedByTheMigration: a database that already holds a (customer, key, lot) twice, which the code cannot write but an earlier bug could have,
// gets migration 00004 without a failure: the earliest row keeps its key, the later ones are renamed, and nothing else about them changes.
func testRepeatedLedgerKeysAreRenamedByTheMigration(t *testing.T) {
	infra(t)
	ctx := testkit.Ctx(t)
	db := testkit.CreateCellDB(t, shared.pg, "billing", "dedupe")
	for _, v := range []int64{1, 2, 3} {
		if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, v, true); err != nil {
			t.Fatal(err)
		}
	}
	tid, customer, lot := uuid.New(), uuid.New(), uuid.New()
	first, second, other := uuid.New(), uuid.New(), uuid.New()
	conn, err := pgx.Connect(ctx, db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	// The owner is under forced row-level security, so the tenant is the one the transaction names.
	inTenant := func(fn func(tx pgx.Tx) error) {
		t.Helper()
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tid.String()); err != nil {
			t.Fatal(err)
		}
		if err := fn(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	inTenant(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO credit_lots (tenant_id, id, customer_id, granted, remaining, source) VALUES ($1,$2,$3,10,10,'manual')`, tid, lot, customer); err != nil {
			return err
		}
		for i, id := range []uuid.UUID{first, second, other} {
			key := "repeated"
			if id == other {
				key = "single"
			}
			if _, err := tx.Exec(ctx, `INSERT INTO credit_ledger (tenant_id, id, customer_id, lot_id, kind, amount, idempotency_key, created_at) VALUES ($1,$2,$3,$4,'consume',-1,$5,now() + $6::int * interval '1 second')`,
				tid, id, customer, lot, key, i); err != nil {
				return err
			}
		}
		return nil
	})

	if err := pgxx.MigrateVersion(ctx, db.Migrator, migrations.FS, 4, true); err != nil {
		t.Fatalf("migration 00004 on data that repeats a key: %v", err)
	}
	keys := map[uuid.UUID]string{}
	inTenant(func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, idempotency_key FROM credit_ledger WHERE tenant_id = $1`, tid)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var k string
			if err := rows.Scan(&id, &k); err != nil {
				return err
			}
			keys[id] = k
		}
		return rows.Err()
	})
	if keys[first] != "repeated" || keys[second] != "repeated#dup-"+second.String() || keys[other] != "single" || len(keys) != 3 {
		t.Errorf("keys after the migration = %v", keys)
	}
	var unique, forced bool
	if err := conn.QueryRow(ctx, `SELECT i.indisunique, c.relforcerowsecurity FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid JOIN pg_class ic ON ic.oid = i.indexrelid
		WHERE ic.relname = 'credit_ledger_key_once'`).Scan(&unique, &forced); err != nil || !unique || !forced {
		t.Errorf("unique index = %t, rows still under forced row-level security = %t, %v", unique, forced, err)
	}
}
