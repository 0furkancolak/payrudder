// Package creditnotes serves the credit-note methods of BillingService and BillingAdminService: it maps requests to commands and queries.
package creditnotes

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Handler implements the credit-note methods of billingv1.BillingServiceServer and billingv1.BillingAdminServiceServer.
type Handler struct {
	app    *pgxpool.Pool
	system *pgxpool.Pool
	notes  *command.Commands
}

// New returns the handler over the billing_app and billing_system pools.
func New(app, system *pgxpool.Pool, notes *command.Commands) *Handler {
	return &Handler{app: app, system: system, notes: notes}
}

// CreateCreditNote implements billingv1.BillingAdminServiceServer.
func (h *Handler) CreateCreditNote(ctx context.Context, req *billingv1.CreateCreditNoteRequest) (*billingv1.CreateCreditNoteResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := errkey.ParseUUID("billing.tenant.bad_id", "tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	iid, err := errkey.ParseUUID("billing.invoice.bad_id", "invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	cn, err := h.notes.Create(ctx, tid, iid, req.GetAmountMinor(), req.GetReason(), req.GetRefundToCard())
	if err != nil {
		return nil, err
	}
	return &billingv1.CreateCreditNoteResponse{CreditNote: creditNoteProto(cn, query.CustomerExternalID(ctx, h.system, tid, iid))}, nil
}

// RetryCreditNoteRefund implements billingv1.BillingAdminServiceServer.
func (h *Handler) RetryCreditNoteRefund(ctx context.Context, req *billingv1.RetryCreditNoteRefundRequest) (*billingv1.RetryCreditNoteRefundResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := errkey.ParseUUID("billing.tenant.bad_id", "tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	id, err := errkey.ParseUUID("billing.credit_note.bad_id", "credit_note_id", req.GetCreditNoteId())
	if err != nil {
		return nil, err
	}
	cn, err := h.notes.RetryRefund(ctx, tid, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.RetryCreditNoteRefundResponse{CreditNote: creditNoteProto(cn, query.CustomerExternalID(ctx, h.system, tid, cn.InvoiceID))}, nil
}

// GetCreditNote implements billingv1.BillingServiceServer.
func (h *Handler) GetCreditNote(ctx context.Context, req *billingv1.GetCreditNoteRequest) (*billingv1.GetCreditNoteResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := errkey.ParseUUID("billing.credit_note.bad_id", "credit_note_id", req.GetCreditNoteId())
	if err != nil {
		return nil, err
	}
	row, err := query.Get(ctx, h.app, ti.ID, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetCreditNoteResponse{CreditNote: creditNoteRowProto(row)}, nil
}

// ListCreditNotes implements billingv1.BillingServiceServer: newest first.
func (h *Handler) ListCreditNotes(ctx context.Context, req *billingv1.ListCreditNotesRequest) (*billingv1.ListCreditNotesResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	page, err := query.List(ctx, h.app, ti.ID, query.ListParams{
		PageSize: req.GetPageSize(), InvoiceID: req.GetInvoiceId(), CustomerExternalID: req.GetCustomerExternalId(), PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, err
	}
	resp := &billingv1.ListCreditNotesResponse{NextPageToken: page.NextPageToken}
	for _, r := range page.CreditNotes {
		resp.CreditNotes = append(resp.CreditNotes, creditNoteRowProto(sqlc.GetCreditNoteRow(r)))
	}
	return resp, nil
}

var refundStates = map[string]billingv1.RefundState{
	model.RefundRequested: billingv1.RefundState_REFUND_STATE_REQUESTED,
	model.RefundRefunded:  billingv1.RefundState_REFUND_STATE_REFUNDED,
	model.RefundFailed:    billingv1.RefundState_REFUND_STATE_FAILED,
}

func creditNoteProto(c *model.CreditNote, customerExternalID string) *billingv1.CreditNote {
	out := &billingv1.CreditNote{
		Id: c.ID.String(), Number: c.Number, InvoiceId: c.InvoiceID.String(), InvoiceNumber: c.InvoiceNumber, CustomerId: c.CustomerID.String(),
		CustomerExternalId: customerExternalID, Currency: c.Currency, TotalMinor: c.TotalMinor, Reason: c.Reason, CreateTime: timestamppb.New(c.CreatedAt),
		Disposition: billingv1.CreditNoteDisposition_CREDIT_NOTE_DISPOSITION_CREDIT, RefundError: c.RefundError,
	}
	if c.Refund {
		out.Disposition, out.RefundState = billingv1.CreditNoteDisposition_CREDIT_NOTE_DISPOSITION_REFUND, refundStates[c.RefundState]
	}
	return out
}

func creditNoteRowProto(r sqlc.GetCreditNoteRow) *billingv1.CreditNote {
	return creditNoteProto(&model.CreditNote{
		ID: r.ID, CustomerID: r.CustomerID, InvoiceID: r.InvoiceID, Number: r.Number, InvoiceNumber: r.InvoiceNumber, Currency: r.Currency, TotalMinor: r.TotalMinor,
		Reason: r.Reason, CreatedAt: r.CreatedAt, Refund: r.Refund, RefundState: r.RefundState, RefundError: r.RefundError,
	}, r.CustomerExternalID)
}
