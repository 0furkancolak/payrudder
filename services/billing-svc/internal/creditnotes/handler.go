// Package creditnotes serves the credit-note methods of BillingService and BillingAdminService: it maps requests to commands and queries.
package creditnotes

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/creditnotes/command"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/creditnotes/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Handler implements the credit-note methods of billingv1.BillingServiceServer and billingv1.BillingAdminServiceServer.
type Handler struct {
	app    *pgxpool.Pool
	system *pgxpool.Pool
	runs   *runs.Manager
}

// New returns the handler over the billing_app and billing_system pools.
func New(app, system *pgxpool.Pool, mgr *runs.Manager) *Handler {
	return &Handler{app: app, system: system, runs: mgr}
}

func invalid(msg string) error { return status.Error(codes.InvalidArgument, msg) }

// CreateCreditNote implements billingv1.BillingAdminServiceServer.
func (h *Handler) CreateCreditNote(ctx context.Context, req *billingv1.CreateCreditNoteRequest) (*billingv1.CreateCreditNoteResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, invalid("tenant_id must be a UUID")
	}
	iid, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		return nil, invalid("invoice_id must be a UUID")
	}
	cn, err := command.Create(ctx, h.runs, tid, iid, req.GetAmountMinor(), req.GetReason(), req.GetRefundToCard())
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
	tid, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, invalid("tenant_id must be a UUID")
	}
	id, err := uuid.Parse(req.GetCreditNoteId())
	if err != nil {
		return nil, invalid("credit_note_id must be a UUID")
	}
	cn, err := command.RetryRefund(ctx, h.runs, tid, id)
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
	id, err := uuid.Parse(req.GetCreditNoteId())
	if err != nil {
		return nil, invalid("credit_note_id must be a UUID")
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
	runs.RefundRequested: billingv1.RefundState_REFUND_STATE_REQUESTED,
	runs.RefundRefunded:  billingv1.RefundState_REFUND_STATE_REFUNDED,
	runs.RefundFailed:    billingv1.RefundState_REFUND_STATE_FAILED,
}

func creditNoteProto(c *runs.CreditNote, customerExternalID string) *billingv1.CreditNote {
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
	return creditNoteProto(&runs.CreditNote{
		ID: r.ID, CustomerID: r.CustomerID, InvoiceID: r.InvoiceID, Number: r.Number, InvoiceNumber: r.InvoiceNumber, Currency: r.Currency, TotalMinor: r.TotalMinor,
		Reason: r.Reason, CreatedAt: r.CreatedAt, Refund: r.Refund, RefundState: r.RefundState, RefundError: r.RefundError,
	}, r.CustomerExternalID)
}
