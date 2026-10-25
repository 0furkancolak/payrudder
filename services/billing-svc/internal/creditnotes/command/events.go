package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/model"
	einvoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/command"
	einvoicemodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// creditNoteEvent announces a credit note (or the outcome of its refund) to the tenant's endpoints.
func creditNoteEvent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, typ, key string, at time.Time, cn *model.CreditNote) error {
	external, err := sqlc.New(tx).GetInvoiceCustomerExternalID(ctx, sqlc.GetInvoiceCustomerExternalIDParams{TenantID: tenantID, ID: cn.InvoiceID})
	if err != nil {
		return err
	}
	data := map[string]any{
		"credit_note_id": cn.ID.String(), "number": cn.Number, "invoice_id": cn.InvoiceID.String(), "invoice_number": cn.InvoiceNumber, "customer_id": cn.CustomerID.String(),
		"customer_external_id": external, "currency": cn.Currency, "total_minor": fmt.Sprint(cn.TotalMinor), "reason": cn.Reason, "disposition": model.Disposition(cn.Refund),
	}
	if cn.Refund {
		data["refund_state"] = cn.RefundState
		if cn.RefundError != "" {
			data["refund_error"] = cn.RefundError
		}
	}
	return tenantevents.Write(ctx, tx, tenantID, typ, key, at, data)
}

// einvoiceCreditNote sends the credit note as an e-document when the invoice it corrects has one that is still alive (pending or issued). An invoice that was never
// e-invoiced (the setting was off, or its document failed for good) leaves nothing for the note to refer to, so the note is not e-invoiced either.
func (c *Commands) einvoiceCreditNote(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, cn *model.CreditNote, environment string) error {
	orig, err := c.q.WithTx(tx).GetEInvoiceOfInvoice(ctx, sqlc.GetEInvoiceOfInvoiceParams{TenantID: tenantID, InvoiceID: cn.InvoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if orig.Status != einvoicemodel.Pending && orig.Status != einvoicemodel.Issued {
		return nil
	}
	cmd, on, err := c.cfg.Engine.CreditNoteSnapshot(ctx, tx, tenantID, cn.InvoiceID, cn.ID, cn.Number, cn.TotalMinor, cn.CreatedAt)
	if err != nil {
		return err
	}
	if on == "" || on == "off" {
		return nil
	}
	_, err = einvoicecmd.Enqueue(ctx, tx, c.cfg.Now(), tenantID, environment, einvoicemodel.KindCreditNote, cn.InvoiceID, &cn.ID, cn.Number, cmd)
	return err
}
