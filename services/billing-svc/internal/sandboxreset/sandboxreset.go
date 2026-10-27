// Package sandboxreset deletes a sandbox tenant's invoices and everything that hangs on them (phase 45 E). The list of what goes is queries/reset.sql; what stays on
// purpose is Kept. One transaction: a failure leaves the tenant as it was.
package sandboxreset

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Kept are the tables with tenant rows that a reset leaves alone: the tenant's configuration. The invoice inbox (its address, the vendor rules and the mail
// received on it) is the tenant's own setup, not something its customers made: the address keeps working, and billing_system may not delete from it.
var Kept = []string{"credit_packs", "credit_settings", "dunning_settings", "expense_inboxes", "expense_inbox_items", "expense_rules"}

type step struct {
	table string
	run   func(q *sqlc.Queries, id uuid.UUID) (int64, error)
}

// steps is the order of the foreign keys: children before parents. payment_links appears once, as the table; its children go first inside it.
func steps(ctx context.Context) []step {
	return []step{
		{"credit_applications", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetCreditApplications(ctx, id) }},
		{"credit_ledger", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetCreditLedger(ctx, id) }},
		{"credit_lots", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetCreditLots(ctx, id) }},
		{"credit_balances", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetCreditBalances(ctx, id) }},
		{"rerating_jobs", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetReratingJobs(ctx, id) }},
		{"saga_events", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetSagaEvents(ctx, id) }},
		{"invoice_sagas", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetInvoiceSagas(ctx, id) }},
		{"invoice_lines", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetInvoiceLines(ctx, id) }},
		{"invoice_payments", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetInvoicePayments(ctx, id) }},
		{"promotion_reversals", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetPromotionReversals(ctx, id) }},
		{"einvoice_documents", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetEInvoiceDocuments(ctx, id) }},
		{"credit_notes", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetCreditNotes(ctx, id) }},
		{"payment_links", func(q *sqlc.Queries, id uuid.UUID) (int64, error) {
			children, err := q.ResetPaymentLinkChildren(ctx, id)
			if err != nil {
				return 0, err
			}
			rest, err := q.ResetPaymentLinks(ctx, id)
			return children + rest, err
		}},
		{"portal_links", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetPortalLinks(ctx, id) }},
		{"invoices", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetInvoices(ctx, id) }},
		{"billing_runs", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetBillingRuns(ctx, id) }},
		{"invoice_number_seq", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetInvoiceNumberSeq(ctx, id) }},
		{"credit_note_seq", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetCreditNoteSeq(ctx, id) }},
		{"outbox", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetOutbox(ctx, id) }},
		{"tenant_event_outbox", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetTenantEventOutbox(ctx, id) }},
		{"expenses", func(q *sqlc.Queries, id uuid.UUID) (int64, error) { return q.ResetExpenses(ctx, id) }},
	}
}

// Tables are the tables a reset deletes from.
func Tables() []string {
	var out []string
	for _, s := range steps(context.Background()) {
		out = append(out, s.table)
	}
	return out
}

// Resetter deletes on the system pool (billing_system), which sees every tenant: each statement names the tenant.
type Resetter struct{ pool *pgxpool.Pool }

// New returns a Resetter.
func New(systemPool *pgxpool.Pool) *Resetter { return &Resetter{pool: systemPool} }

// Reset deletes the tenant's data and returns the rows deleted by table (tables with none are left out).
func (r *Resetter) Reset(ctx context.Context, tenantID uuid.UUID) (map[string]int64, error) {
	out := map[string]int64{}
	err := pgxx.InTx(ctx, r.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		for _, s := range steps(ctx) {
			n, err := s.run(q, tenantID)
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
