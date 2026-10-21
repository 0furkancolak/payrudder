package runs

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// invoice finalizes every draft of the run (number, open, saga started) and reports whether the run is complete:
// all its invoices' sagas reached COMPLETED or VOIDED.
func (m *Manager) invoice(ctx context.Context, run *Run) (bool, error) {
	for {
		ids, err := m.q.ListDraftInvoiceIDsOfRun(ctx, sqlc.ListDraftInvoiceIDsOfRunParams{TenantID: run.TenantID, PeriodID: run.PeriodID, PageSize: int32(m.cfg.PageSize)}) //nolint:gosec // small
		if err != nil {
			return false, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := m.Finalize(ctx, run.TenantID, id); err != nil {
				return false, err
			}
		}
	}
	open, err := m.q.CountUnsettledInvoicesOfRun(ctx, sqlc.CountUnsettledInvoicesOfRunParams{TenantID: run.TenantID, PeriodID: run.PeriodID})
	if err != nil {
		return false, err
	}
	return open == 0, nil
}

// Finalize assigns the next gap-free number of the tenant and year, opens the invoice and starts its saga, in one
// transaction: a rollback leaves neither a number consumed nor a saga behind. Finalizing a finalized invoice is a no-op.
func (m *Manager) Finalize(ctx context.Context, tenantID, invoiceID uuid.UUID) error {
	return pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := m.q.WithTx(tx)
		inv, err := q.LockInvoiceForFinalize(ctx, sqlc.LockInvoiceForFinalizeParams{TenantID: tenantID, ID: invoiceID})
		if err != nil {
			return err
		}
		if inv.Status != "draft" {
			return nil
		}
		now := m.cfg.Now()
		year := int32(now.UTC().Year()) //nolint:gosec // a calendar year
		if err := q.EnsureInvoiceNumberSeq(ctx, sqlc.EnsureInvoiceNumberSeqParams{TenantID: tenantID, Year: year}); err != nil {
			return err
		}
		seq, err := q.LockInvoiceNumberSeq(ctx, sqlc.LockInvoiceNumberSeqParams{TenantID: tenantID, Year: year})
		if err != nil {
			return err
		}
		if err := q.BumpInvoiceNumberSeq(ctx, sqlc.BumpInvoiceNumberSeqParams{TenantID: tenantID, Year: year}); err != nil {
			return err
		}
		number := Number(inv.Environment, int(year), seq)
		if err := q.OpenInvoice(ctx, sqlc.OpenInvoiceParams{Number: number, FinalizedAt: now, TenantID: tenantID, ID: invoiceID}); err != nil {
			return err
		}
		if err := saga.FinalizedFact(ctx, tx, now, tenantID, invoiceID, inv.CustomerID, inv.PeriodID, number, inv.Currency, inv.TotalMinor); err != nil {
			return err
		}
		if err := saga.TenantEvent(ctx, tx, tenantID, saga.EventInvoiceFinalized, invoiceID.String(), now,
			saga.InvoiceData(invoiceID, inv.CustomerID, inv.CustomerExternalID, number, inv.PeriodID, inv.Currency, inv.TotalMinor, "open")); err != nil {
			return err
		}
		sagaID, err := m.cfg.Engine.Start(ctx, tx, tenantID, invoiceID)
		if err != nil {
			return err
		}
		return m.cfg.Engine.EInvoiceOnFinalize(ctx, tx, tenantID, sagaID)
	})
}

// Number formats an invoice number: INV-2026-000123 for live tenants, SBX-2026-000123 for sandbox ones, so a sandbox
// invoice can't be mistaken for a real one.
func Number(environment string, year int, seq int64) string {
	prefix := "INV"
	if environment == "sandbox" {
		prefix = "SBX"
	}
	return fmt.Sprintf("%s-%d-%06d", prefix, year, seq)
}
