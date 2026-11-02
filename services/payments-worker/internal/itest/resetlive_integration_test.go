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
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	paymentsreset "github.com/0furkancolak/payrudder/services/payments-worker/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// A sandbox reset deletes from ten tables, and the database is asked first whether the tenant has a row of the live environment in any of them, not only in the ledger and
// the attempts. A tenant with an e-invoice, a provider subscription or a notification of the live environment is refused with nothing deleted; the same rows of a sandbox
// tenant go. (A notification has no environment of its own: a tenant with a live provider account is a live tenant.)
func TestAResetIsRefusedForLiveRowsInEveryTableItDeletesFrom(t *testing.T) {
	w := openDB(t, testkit.StartPostgres(t))
	ctx := context.Background()
	rows := map[string]string{
		"einvoice_issuances":     `INSERT INTO einvoice_issuances (document_id, tenant_id, environment, provider, kind, document_type, status) VALUES (gen_random_uuid(), $1, $2, 'nilvera', 'invoice', 'e-fatura', 'started')`,
		"provider_subscriptions": `INSERT INTO provider_subscriptions (tenant_id, provider, environment, provider_ref, status) VALUES ($1, 'paddle', $2, 'sub_1', 'active')`,
		"provider_events":        `INSERT INTO provider_events (provider, event_id, tenant_id, body, status) VALUES ('paddle', 'evt_' || $2::text || gen_random_uuid()::text, $1, '{}', 'processed')`,
	}
	for table, insert := range rows {
		t.Run(table, func(t *testing.T) {
			live, sandbox := uuid.New(), uuid.New()
			for _, tn := range []struct {
				id  uuid.UUID
				env string
			}{{live, "live"}, {sandbox, "sandbox"}} {
				if _, err := w.At(tn.id).Exec(ctx, insert, tn.id, tn.env); err != nil {
					t.Fatal(err)
				}
				// Where the row has no environment of its own, the tenant's account says: a live account for the live tenant, a test one for the sandbox.
				if _, err := w.At(tn.id).Exec(ctx, `INSERT INTO provider_accounts (tenant_id, provider, environment, settings_enc) VALUES ($1, 'paddle', $2, '\x00')`, tn.id, tn.env); err != nil {
					t.Fatal(err)
				}
			}
			_, err := paymentsreset.New(w.DB).Reset(ctx, live)
			var re *refuse.Error
			if !errors.As(err, &re) || re.Status != http.StatusConflict || re.Key != "admin.sandbox.live_refused" {
				t.Fatalf("a reset of a tenant with a live %s row: %v, want a 409 admin.sandbox.live_refused", table, err)
			}
			var n int
			if err := w.At(live).QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, live).Scan(&n); err != nil || n != 1 {
				t.Errorf("%s of the live tenant after the refused reset: %d, %v; want its row", table, n, err)
			}
			out, err := paymentsreset.New(w.DB).Reset(ctx, sandbox)
			if err != nil || out[table] != 1 {
				t.Errorf("a reset of a sandbox tenant: %v, %v; want its %s row deleted", out, err, table)
			}
		})
	}
}

// The e-invoices an integrator accepted are legal documents: like the ledger, they are not deleted, except by a reset of the sandbox (a sandbox document only) or by the move of
// the tenant to another place.
func TestEInvoiceIssuancesAreNotDeletedExceptByAResetOrAMove(t *testing.T) {
	w := openDB(t, testkit.StartPostgres(t))
	ctx := context.Background()
	live, sandbox := uuid.New(), uuid.New()
	for _, tn := range []struct {
		id  uuid.UUID
		env string
	}{{live, "live"}, {sandbox, "sandbox"}} {
		if _, err := w.At(tn.id).Exec(ctx, `INSERT INTO einvoice_issuances (document_id, tenant_id, environment, provider, kind, document_type, status, legal_number)
			VALUES (gen_random_uuid(), $1, $2, 'nilvera', 'invoice', 'e-fatura', 'issued', 'ABC2026000000001')`, tn.id, tn.env); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(err error) bool {
		var pe *pgconn.PgError
		return errors.As(err, &pe) && pe.Code == pgxx.CodeRaiseException && pe.Hint == "ledger_row_delete"
	}
	inTx := func(tid uuid.UUID, settings ...string) error {
		return store.Tx(ctx, w.DB, tid, func(tx pgx.Tx, _ *sqlc.Queries, _ string) error {
			for _, s := range settings {
				if _, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, s, tid.String()); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `DELETE FROM einvoice_issuances WHERE tenant_id = $1`, tid); err != nil {
				return err
			}
			return errRollBack
		})
	}
	for _, tid := range []uuid.UUID{live, sandbox} {
		if _, err := w.At(tid).Exec(ctx, `DELETE FROM einvoice_issuances WHERE tenant_id = $1`, tid); !refused(err) {
			t.Errorf("DELETE of the e-invoices of tenant %s without a reset or a move: %v, want the ledger_row_delete refusal", tid, err)
		}
	}
	if err := inTx(live, "payrudder.sandbox_reset"); !refused(err) {
		t.Errorf("a reset deleted the e-invoices of a live tenant: %v", err)
	}
	if err := inTx(sandbox, "payrudder.sandbox_reset"); !errors.Is(err, errRollBack) {
		t.Errorf("a reset could not delete the e-invoices of a sandbox tenant: %v", err)
	}
	if err := inTx(live, "payrudder.tenant_move"); !errors.Is(err, errRollBack) {
		t.Errorf("a move could not delete the e-invoices of a live tenant: %v", err)
	}
	if err := inTx(sandbox); !refused(err) {
		t.Errorf("the e-invoices of a sandbox tenant were deleted in a transaction that names no reset: %v", err)
	}
	// Updating a document (it is cancelled, or its files are set) is not a delete and stays possible.
	if _, err := w.At(live).Exec(ctx, `UPDATE einvoice_issuances SET status = 'cancelled' WHERE tenant_id = $1`, live); err != nil {
		t.Errorf("cancelling a document: %v", err)
	}
}
