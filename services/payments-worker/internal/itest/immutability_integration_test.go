//go:build integration

package itest

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	paymentsreset "github.com/0furkancolak/payrudder/services/payments-worker/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// errRollBack ends a transaction of a test that only looked.
var errRollBack = errors.New("roll back")

// The payment ledger and the receipts recorded by hand are not deleted: not by payments_system (the role that may, for the sandbox reset), unless the transaction names the
// tenant as being reset or moved; and a reset does not delete a row of the live environment whatever the caller said. payments_system has no UPDATE on them either.
func TestLedgerAndReceiptsAreNotDeletedExceptByAResetOrAMove(t *testing.T) {
	w := openDB(t, testkit.StartPostgres(t))
	ctx := context.Background()
	ledger, attempts := store.Ledger{DB: w.DB}, store.Attempts{DB: w.DB}
	live, sandbox := uuid.New(), uuid.New()
	for _, tn := range []struct {
		tid uuid.UUID
		env string
	}{{live, "live"}, {sandbox, "sandbox"}} {
		inv := uuid.New()
		if _, err := attempts.Start(ctx, uuid.New(), tn.tid, inv, "charge", tn.env, "mockpsp", inv.String()+":charge:0", map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(ctx, store.LedgerEntry{TenantID: tn.tid, InvoiceID: inv, Provider: "mockpsp", Environment: tn.env, Kind: "charge", Status: "succeeded",
			AmountMinor: 100, Currency: "USD", ProviderRef: "ch_" + inv.String(), Source: "command"}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.At(tn.tid).Exec(ctx, `INSERT INTO manual_receipts (tenant_id, invoice_id, ledger_id, bank_reference, amount_minor, currency, received_at, recorded_by)
			SELECT tenant_id, invoice_id, id, 'ref-1', 100, 'USD', now(), 'test' FROM payment_ledger WHERE tenant_id = $1`, tn.tid); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(err error) bool {
		var pe *pgconn.PgError
		return errors.As(err, &pe) && pe.Code == pgxx.CodeRaiseException && pe.Hint == "ledger_row_delete"
	}
	// inTx deletes from table in a transaction of the tenant's place that sets the given settings first, and rolls back.
	inTx := func(tid uuid.UUID, table string, settings ...string) error {
		err := store.Tx(ctx, w.DB, tid, func(tx pgx.Tx, _ *sqlc.Queries, _ string) error {
			for _, s := range settings {
				if _, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, s, tid.String()); err != nil {
					return err
				}
			}
			if table == "payment_ledger" { // the receipts point at the ledger rows: they go first, as in a reset
				if _, err := tx.Exec(ctx, `DELETE FROM manual_receipts WHERE tenant_id = $1`, tid); err != nil {
					return err
				}
			}
			// table is one of the constants below, never input.
			if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE tenant_id = $1`, tid); err != nil {
				return err
			}
			return errRollBack
		})
		return err
	}
	for _, table := range []string{"manual_receipts", "payment_ledger"} {
		for _, tid := range []uuid.UUID{live, sandbox} {
			if _, err := w.At(tid).Exec(ctx, `DELETE FROM `+table+` WHERE tenant_id = $1`, tid); !refused(err) {
				t.Errorf("DELETE FROM %s of tenant %s without a reset or a move: %v, want the ledger_row_delete refusal", table, tid, err)
			}
		}
		if err := inTx(live, table, "payrudder.sandbox_reset"); !refused(err) {
			t.Errorf("a reset deleted %s of a live tenant: %v", table, err)
		}
		if err := inTx(sandbox, table, "payrudder.sandbox_reset"); !errors.Is(err, errRollBack) {
			t.Errorf("a reset could not delete %s of a sandbox tenant: %v", table, err)
		}
		if err := inTx(live, table, "payrudder.tenant_move"); !errors.Is(err, errRollBack) {
			t.Errorf("a move could not delete %s of a live tenant: %v", table, err)
		}
		if err := inTx(sandbox, table); !refused(err) {
			t.Errorf("%s of a sandbox tenant was deleted in a transaction that names no reset: %v", table, err)
		}
	}
	// A setting that names another tenant is no leave.
	err := store.Tx(ctx, w.DB, sandbox, func(tx pgx.Tx, _ *sqlc.Queries, _ string) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('payrudder.sandbox_reset', $1, true), set_config('payrudder.tenant_move', $1, true)`, live.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM payment_ledger WHERE tenant_id = $1`, sandbox)
		return err
	})
	if !refused(err) {
		t.Errorf("a delete that names another tenant: %v", err)
	}

	// A reset that is asked for a tenant with a live row is refused before it deletes anything.
	_, err = paymentsreset.New(w.DB).Reset(ctx, live)
	var re *refuse.Error
	if !errors.As(err, &re) || re.Status != http.StatusConflict || re.Key != "admin.sandbox.live_refused" {
		t.Errorf("a reset of a live tenant: %v, want a 409 admin.sandbox.live_refused", err)
	}
	for _, table := range []string{"payment_ledger", "manual_receipts", "payment_attempts"} {
		var n int
		if err := w.At(live).QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, live).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s of the live tenant after a refused reset: %d, %v", table, n, err)
		}
	}
	// The sandbox's own reset takes the receipts and the ledger.
	out, err := paymentsreset.New(w.DB).Reset(ctx, sandbox)
	if err != nil || out["payment_ledger"] != 1 || out["manual_receipts"] != 1 {
		t.Errorf("a reset of a sandbox tenant: %v, %v", out, err)
	}

	// Grants: no UPDATE on either table.
	for _, stmt := range []string{`UPDATE payment_ledger SET amount_minor = 1 WHERE tenant_id = $1`, `UPDATE manual_receipts SET amount_minor = 1 WHERE tenant_id = $1`} {
		if _, err := w.At(live).Exec(ctx, stmt, live); pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
			t.Errorf("payments_system: %s: %v, want permission denied", stmt, err)
		}
	}
}

// payments_app has no table of payments_db, public or a tenant's schema in a cell, nor the shared index of charge keys (migrations 00007 and 00008): payments-worker
// connects as payments_system only, and these tables have no row-level security, so a SELECT of the app role would read every tenant's attempts, ledger and audit rows.
func TestTheAppRoleCannotReadAnyTable(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	pub := testkit.MigratedDB(t, pg, "payments", migrations.FS)
	cell := testkit.CreateCellDB(t, pg, "payments", "c1")
	id := uuid.New()
	if _, err := pgxx.ProvisionTenant(ctx, cell.Migrator, tenantSchemas(), id, nil); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ migrator, schema string }{
		"public": {pub.Migrator, "public"}, "a tenant's schema": {cell.Migrator, tenantdir.Schema(id)}, "the shared schema": {cell.Migrator, pgxx.SharedSchema},
	} {
		conn, err := pgx.Connect(ctx, c.migrator)
		if err != nil {
			t.Fatal(err)
		}
		var tables int
		var readable []string
		err = conn.QueryRow(ctx, `SELECT count(*), COALESCE(array_agg(c.relname) FILTER (WHERE has_table_privilege('payments_app', c.oid, 'SELECT')), '{}')
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition`, c.schema).Scan(&tables, &readable)
		_ = conn.Close(ctx)
		if err != nil || tables == 0 {
			t.Fatalf("%s: %d tables, %v", name, tables, err)
		}
		if len(readable) > 0 {
			t.Errorf("%s: payments_app can read %v", name, readable)
		}
	}
	app, err := pgx.Connect(ctx, pub.App)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(ctx) }()
	if _, err := app.Exec(ctx, `SELECT 1 FROM payment_ledger`); pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
		t.Errorf("payments_app reading the ledger: %v, want permission denied", err)
	}
}
