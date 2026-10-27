// Package sandboxreset deletes a sandbox tenant's payment attempts, ledger and receipts (phase 45 E). Its provider accounts and the objects created at providers stay.
package sandboxreset

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Tables are the tables a reset deletes from; Kept are the tables with tenant rows it leaves alone on purpose.
var (
	Tables = []string{"manual_receipts", "payment_ledger", "payment_attempts", "provider_subscriptions", "provider_events", "einvoice_issuances"}
	Kept   = []string{"provider_accounts", "provider_account_events", "provider_objects", "einvoice_accounts"}
)

// Resetter deletes on the system role, which sees every tenant of the pooled database, so each statement names the tenant. When cells are on (ADR 0018) it is
// built over the system-role router and a tenant placed in a cell is reset in its own schema, in a transaction that is that tenant's alone.
type Resetter struct{ db store.DB }

// New returns a Resetter over the system pool, or over the system-role router when cells are on.
func New(db store.DB) *Resetter { return &Resetter{db: db} }

// Reset deletes the tenant's rows in one transaction and returns how many, by table (tables with none are left out). The ledger rows go after the receipts that
// point at them. A tenant that is moving is not reset (the error matches tenantdir.ErrNotServing): a reset must not run where the tenant's rows are being copied.
func (r *Resetter) Reset(ctx context.Context, tenantID uuid.UUID) (map[string]int64, error) {
	out := map[string]int64{}
	err := store.Tx(ctx, r.db, tenantID, func(tx pgx.Tx, q *sqlc.Queries, schema string) error {
		for _, s := range []struct {
			table string
			run   func(context.Context, uuid.UUID) (int64, error)
		}{
			{"manual_receipts", q.ResetManualReceipts}, {"payment_ledger", q.ResetPaymentLedger}, {"payment_attempts", q.ResetPaymentAttempts},
			{"provider_subscriptions", q.ResetProviderSubscriptions}, {"provider_events", func(ctx context.Context, id uuid.UUID) (int64, error) { return q.ResetProviderEvents(ctx, &id) }}, {"einvoice_issuances", q.ResetEInvoiceIssuances},
		} {
			n, err := s.run(ctx, tenantID)
			if err != nil {
				return err
			}
			if n > 0 {
				out[s.table] = n
			}
		}
		if schema != "" {
			// The attempts are gone, and so are their keys in the index of the cell that finds a notification's tenant.
			if _, err := tx.Exec(ctx, `DELETE FROM shared.charge_keys WHERE tenant_id = $1`, tenantID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
