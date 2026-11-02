package command

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/model"
	creditscommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/command"
	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
	sagamodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// create issues a credit note against a paid invoice and adds its amount to the customer's credit balance, or, with refund, asks the invoice's
// payment provider to return it to the card; either way in one transaction: a rollback leaves neither a number consumed nor a credit or refund behind.
// amount 0 means whatever is left of the invoice. The notes of an invoice never exceed it.
func (c *Commands) create(ctx context.Context, tenantID, invoiceID uuid.UUID, amount int64, reason string, refund bool) (*model.CreditNote, error) {
	var out *model.CreditNote
	err := c.cfg.DB.InTx(ctx, tenantID, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := c.q.WithTx(tx)
		// The saga first (the order a void takes them in): a void that has begun refunds the whole invoice, so no note can start next to it.
		switch state, err := q.LockSagaStateByInvoice(ctx, sqlc.LockSagaStateByInvoiceParams{TenantID: tenantID, InvoiceID: invoiceID}); {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case slices.Contains([]sagamodel.State{sagamodel.RefundRequested, sagamodel.RefundFailed, sagamodel.Refunded, sagamodel.Voided}, sagamodel.State(state)):
			return fmt.Errorf("%w (the invoice is being voided)", model.ErrNotPaid)
		}
		inv, err := q.LockInvoiceForCreditNote(ctx, sqlc.LockInvoiceForCreditNoteParams{TenantID: tenantID, ID: invoiceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return model.ErrInvoiceNotFound
		}
		if err != nil {
			return err
		}
		if inv.Status != invmodel.StatusPaid {
			return model.ErrNotPaid
		}
		// A refund goes back through the provider that took the payment. Money recorded by hand (a bank transfer, cash) never passed through one.
		if refund {
			if received, err := q.SumInvoicePayments(ctx, sqlc.SumInvoicePaymentsParams{TenantID: tenantID, InvoiceID: invoiceID}); err != nil {
				return err
			} else if received > 0 {
				return model.ErrNoRefund
			}
		}
		amount, left, err := creditableAmount(ctx, q, tenantID, invoiceID, inv.TotalMinor, amount)
		if err != nil {
			return err
		}
		tax, err := taxGivenBack(ctx, q, tenantID, invoiceID, inv.TotalMinor, left, amount)
		if err != nil {
			return err
		}
		now := c.cfg.Now()
		year := invmodel.NumberYear(now, inv.TimeZone)
		seq, err := nextCreditNoteSeq(ctx, q, tenantID, year)
		if err != nil {
			return err
		}
		cn := &model.CreditNote{ID: uuid.New(), CustomerID: inv.CustomerID, InvoiceID: invoiceID, Number: model.Number(inv.Environment, int(year), seq), Currency: inv.Currency,
			Reason: reason, TotalMinor: amount, TaxMinor: tax, CreatedAt: now}
		if inv.Number != nil {
			cn.InvoiceNumber = *inv.Number
		}
		if err := q.InsertCreditNote(ctx, sqlc.InsertCreditNoteParams{TenantID: tenantID, ID: cn.ID, CustomerID: inv.CustomerID, InvoiceID: invoiceID, Number: cn.Number,
			Currency: inv.Currency, TotalMinor: amount, TaxMinor: tax, Reason: reason, CreatedAt: now, Disposition: model.Disposition(refund)}); err != nil {
			return err
		}
		credited, err := q.SumCreditNotes(ctx, sqlc.SumCreditNotesParams{TenantID: tenantID, InvoiceID: invoiceID})
		if err != nil {
			return err
		}
		// Credited in full is a full refund of the purchase: a coupon it used gives its redemption back (the scheduler tells plans-svc).
		if credited >= inv.TotalMinor {
			if err := q.QueuePromotionReversal(ctx, sqlc.QueuePromotionReversalParams{TenantID: tenantID, ID: invoiceID}); err != nil {
				return err
			}
		}
		// A credit-pack invoice takes back the share of its credits that the note credits: the whole lot when the notes add up to the invoice (default, not agreed:
		// a partial note takes a proportional part, as far as the customer still has the credits).
		if err := creditscommand.RevokePurchase(ctx, tx, tenantID, invoiceID, credited-amount, credited, inv.TotalMinor, "cn:"+cn.ID.String(), "credit note "+cn.Number+" against invoice "+cn.InvoiceNumber); err != nil {
			return err
		}
		if refund {
			cn.Refund = true
			err = c.requestRefund(ctx, tx, tenantID, cn, inv.Environment, 1, 0, 0, "")
		} else {
			err = q.InsertCreditBalanceForNote(ctx, sqlc.InsertCreditBalanceForNoteParams{TenantID: tenantID, CustomerID: inv.CustomerID, ID: uuid.New(), AmountMinor: amount,
				SourceCreditNoteID: cn.ID, Currency: inv.Currency})
		}
		if err != nil {
			return err
		}
		if err := c.einvoiceCreditNote(ctx, tx, tenantID, cn, inv.Environment); err != nil {
			return err
		}
		out = cn
		how := "added to the customer's credit"
		if refund {
			how = "refunded to the card"
		}
		if err := audit.Record(ctx, tx, audit.Event{TenantID: tenantID, Action: "credit_note.issued", TargetType: "credit_note", TargetID: cn.ID.String(),
			Summary: fmt.Sprintf("issued the credit note %s for invoice %s (%d %s, %s)", cn.Number, cn.InvoiceNumber, amount, cn.Currency, how),
			Detail:  map[string]any{"number": cn.Number, "invoice_id": invoiceID.String(), "amount_minor": amount, "currency": cn.Currency, "refund": refund, "reason": reason}}); err != nil {
			return err
		}
		return creditNoteEvent(ctx, tx, tenantID, tenantevents.EventCreditNoteCreated, cn.ID.String(), now, cn)
	})
	return out, err
}

// creditableAmount is the amount a new credit note takes: what was asked, or what is left of the invoice for 0; left is what the invoice has left to credit.
func creditableAmount(ctx context.Context, q *sqlc.Queries, tenantID, invoiceID uuid.UUID, total, amount int64) (taken, left int64, err error) {
	credited, err := q.SumCreditNotes(ctx, sqlc.SumCreditNotesParams{TenantID: tenantID, InvoiceID: invoiceID})
	if err != nil {
		return 0, 0, err
	}
	left = total - credited
	switch {
	case left <= 0:
		return 0, 0, model.ErrNothingLeft
	case amount == 0:
		return left, left, nil
	case amount < 0 || amount > left:
		return 0, 0, model.ErrBadAmount
	}
	return amount, left, nil
}

// taxGivenBack is the part of a note of amount that is the invoice's tax coming back (model.TaxShare): the invoice's TAX lines against the tax its earlier notes gave back.
func taxGivenBack(ctx context.Context, q *sqlc.Queries, tenantID, invoiceID uuid.UUID, total, left, amount int64) (int64, error) {
	invoiceTax, err := q.SumInvoiceTax(ctx, sqlc.SumInvoiceTaxParams{TenantID: tenantID, InvoiceID: invoiceID})
	if err != nil || invoiceTax <= 0 {
		return 0, err
	}
	before, err := q.SumCreditNoteTax(ctx, sqlc.SumCreditNoteTaxParams{TenantID: tenantID, InvoiceID: invoiceID})
	if err != nil {
		return 0, err
	}
	return model.TaxShare(invoiceTax, total, before, left, amount), nil
}

// nextCreditNoteSeq takes the next gap-free number of the tenant and year.
func nextCreditNoteSeq(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, year int32) (int64, error) {
	if err := q.EnsureCreditNoteSeq(ctx, sqlc.EnsureCreditNoteSeqParams{TenantID: tenantID, Year: year}); err != nil {
		return 0, err
	}
	seq, err := q.LockCreditNoteSeq(ctx, sqlc.LockCreditNoteSeqParams{TenantID: tenantID, Year: year})
	if err != nil {
		return 0, err
	}
	return seq, q.BumpCreditNoteSeq(ctx, sqlc.BumpCreditNoteSeqParams{TenantID: tenantID, Year: year})
}
