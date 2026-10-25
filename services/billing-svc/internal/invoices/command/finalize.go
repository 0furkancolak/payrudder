package command

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// Finalize assigns the next gap-free number of the tenant and year, opens the invoice and starts its saga, in one
// transaction: a rollback leaves neither a number consumed nor a saga behind. Finalizing a finalized invoice is a no-op.
func (c *Commands) Finalize(ctx context.Context, tenantID, invoiceID uuid.UUID) error {
	return pgxx.InTx(ctx, c.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := c.q.WithTx(tx)
		inv, err := q.LockInvoiceForFinalize(ctx, sqlc.LockInvoiceForFinalizeParams{TenantID: tenantID, ID: invoiceID})
		if err != nil {
			return err
		}
		if inv.Status != invmodel.StatusDraft {
			return nil
		}
		now := c.cfg.Now()
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
		if err := sagacommand.FinalizedFact(ctx, tx, now, tenantID, invoiceID, inv.CustomerID, inv.PeriodID, number, inv.Currency, inv.TotalMinor); err != nil {
			return err
		}
		if err := tenantevents.Write(ctx, tx, tenantID, tenantevents.EventInvoiceFinalized, invoiceID.String(), now,
			tenantevents.InvoiceData(invoiceID, inv.CustomerID, inv.CustomerExternalID, number, inv.PeriodID, inv.Currency, inv.TotalMinor, invmodel.StatusOpen)); err != nil {
			return err
		}
		sagaID, err := c.cfg.Engine.Start(ctx, tx, tenantID, invoiceID)
		if err != nil {
			return err
		}
		return c.cfg.Engine.EInvoiceOnFinalize(ctx, tx, tenantID, sagaID)
	})
}

// Number formats an invoice number: INV-2026-000123 for live tenants, SBX-2026-000123 for sandbox ones, so a sandbox
// invoice can't be mistaken for a real one.
func Number(environment string, year int, seq int64) string {
	if environment == env.Sandbox.String() {
		return fmt.Sprintf("%s%d-%06d", env.SandboxNumberPrefix, year, seq)
	}
	return fmt.Sprintf("INV-%d-%06d", year, seq)
}
