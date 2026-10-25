// Package sandboxreset deletes a sandbox tenant's payment attempts, ledger and receipts (phase 45 E). Its provider accounts and the objects created at providers stay.
package sandboxreset

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Tables are the tables a reset deletes from; Kept are the tables with tenant rows it leaves alone on purpose.
var (
	Tables = []string{"manual_receipts", "payment_ledger", "payment_attempts", "provider_subscriptions", "provider_events", "einvoice_issuances"}
	Kept   = []string{"provider_accounts", "provider_account_events", "provider_objects", "einvoice_accounts"}
)

// Resetter deletes on the system pool, which sees every tenant: each statement names the tenant.
type Resetter struct{ pool *pgxpool.Pool }

// New returns a Resetter.
func New(systemPool *pgxpool.Pool) *Resetter { return &Resetter{pool: systemPool} }

// Reset deletes the tenant's rows in one transaction and returns how many, by table (tables with none are left out). The ledger rows go after the receipts that
// point at them.
func (r *Resetter) Reset(ctx context.Context, tenantID uuid.UUID) (map[string]int64, error) {
	out := map[string]int64{}
	err := pgxx.InTx(ctx, r.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
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
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
