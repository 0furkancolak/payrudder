// Package einvoices serves the e-document methods of BillingService and BillingAdminService and applies the worker's answers (docs/phases/phase-30-e-invoice.md).
package einvoices

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/einvoices/command"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/einvoices/query"
	invoicequery "github.com/0furkancolak/quotify/services/billing-svc/internal/invoices/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Handler implements the e-document methods of billingv1.BillingServiceServer and billingv1.BillingAdminServiceServer.
type Handler struct {
	app    *pgxpool.Pool
	system *pgxpool.Pool
	cmd    *command.Commands
	files  invoicequery.PDFSigner
}

// New returns the handler over the billing_app and billing_system pools.
func New(app, system *pgxpool.Pool, cmd *command.Commands) *Handler {
	return &Handler{app: app, system: system, cmd: cmd}
}

// WithFiles enables GetEInvoiceFileUrl.
func (h *Handler) WithFiles(s invoicequery.PDFSigner) *Handler {
	h.files = s
	return h
}

// invalid is an InvalidArgument status with the error key key.
func invalid(key, msg string) error { return errkey.New(codes.InvalidArgument, key, msg) }

func docProto(d sqlc.GetEInvoiceDocumentRow) *billingv1.EInvoiceDocument {
	out := &billingv1.EInvoiceDocument{
		Id: d.ID.String(), Kind: d.Kind, InvoiceId: d.InvoiceID.String(), Number: d.Number, Status: d.Status, DocumentType: d.DocumentType, Provider: d.Provider,
		Environment: d.Environment, LegalNumber: d.LegalNumber, ExternalId: d.ExternalID, Attempts: d.Attempts, LastErrorKey: d.LastErrorKey, LastError: d.LastError,
		HasXml: d.HasXml, HasPdf: d.HasPdf, CreatedAt: timestamppb.New(d.CreatedAt), UpdatedAt: timestamppb.New(d.UpdatedAt),
	}
	if d.CreditNoteID != nil {
		out.CreditNoteId = d.CreditNoteID.String()
	}
	if d.NextAttemptAt != nil {
		out.NextAttemptAt = timestamppb.New(*d.NextAttemptAt)
	}
	if d.IssuedAt != nil {
		out.IssuedAt = timestamppb.New(*d.IssuedAt)
	}
	return out
}

func listProto(d sqlc.ListEInvoiceDocumentsRow) *billingv1.EInvoiceDocument {
	return docProto(sqlc.GetEInvoiceDocumentRow{ID: d.ID, Kind: d.Kind, InvoiceID: d.InvoiceID, CreditNoteID: d.CreditNoteID, Number: d.Number, Status: d.Status,
		DocumentType: d.DocumentType, Provider: d.Provider, Environment: d.Environment, Attempts: d.Attempts, NextAttemptAt: d.NextAttemptAt, LastErrorKey: d.LastErrorKey,
		LastError: d.LastError, LegalNumber: d.LegalNumber, ExternalID: d.ExternalID, HasXml: d.HasXml, HasPdf: d.HasPdf, CreatedAt: d.CreatedAt, IssuedAt: d.IssuedAt, UpdatedAt: d.UpdatedAt})
}

func parseID(field, v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, invalid("billing.einvoice.bad_id", field+" must be a UUID")
	}
	return id, nil
}

// GetEInvoice implements billingv1.BillingServiceServer.
func (h *Handler) GetEInvoice(ctx context.Context, req *billingv1.GetEInvoiceRequest) (*billingv1.GetEInvoiceResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	d, err := query.Get(ctx, h.app, ti.ID, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetEInvoiceResponse{Document: docProto(d)}, nil
}

// ListEInvoices implements billingv1.BillingServiceServer.
func (h *Handler) ListEInvoices(ctx context.Context, req *billingv1.ListEInvoicesRequest) (*billingv1.ListEInvoicesResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	page, err := query.List(ctx, h.app, ti.ID, query.ListParams{PageSize: req.GetPageSize(), InvoiceID: req.GetInvoiceId(), CreditNoteID: req.GetCreditNoteId(),
		Status: req.GetStatus(), PageToken: req.GetPageToken()})
	if err != nil {
		return nil, err
	}
	out := &billingv1.ListEInvoicesResponse{NextPageToken: page.NextPageToken, Total: page.Total}
	for _, d := range page.Documents {
		out.Documents = append(out.Documents, listProto(d))
	}
	return out, nil
}

// GetEInvoiceFileUrl implements billingv1.BillingServiceServer.
func (h *Handler) GetEInvoiceFileUrl(ctx context.Context, req *billingv1.GetEInvoiceFileUrlRequest) (*billingv1.GetEInvoiceFileUrlResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	url, expires, err := query.FileURL(ctx, h.app, h.files, ti.ID, id, req.GetKind())
	if err != nil {
		return nil, err
	}
	return &billingv1.GetEInvoiceFileUrlResponse{Url: url, ExpiresAt: timestamppb.New(expires)}, nil
}

// RetryEInvoice implements billingv1.BillingAdminServiceServer.
func (h *Handler) RetryEInvoice(ctx context.Context, req *billingv1.RetryEInvoiceRequest) (*billingv1.RetryEInvoiceResponse, error) {
	d, err := h.operate(ctx, req.GetTenantId(), req.GetId(), func(tid, id uuid.UUID) error { return h.cmd.Retry(ctx, tid, id) })
	if err != nil {
		return nil, err
	}
	return &billingv1.RetryEInvoiceResponse{Document: d}, nil
}

// CancelEInvoice implements billingv1.BillingAdminServiceServer.
func (h *Handler) CancelEInvoice(ctx context.Context, req *billingv1.CancelEInvoiceRequest) (*billingv1.CancelEInvoiceResponse, error) {
	d, err := h.operate(ctx, req.GetTenantId(), req.GetId(), func(tid, id uuid.UUID) error { return h.cmd.Cancel(ctx, tid, id, req.GetReason()) })
	if err != nil {
		return nil, err
	}
	return &billingv1.CancelEInvoiceResponse{Document: d}, nil
}

func (h *Handler) operate(ctx context.Context, tenantID, docID string, do func(tid, id uuid.UUID) error) (*billingv1.EInvoiceDocument, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := parseID("tenant_id", tenantID)
	if err != nil {
		return nil, err
	}
	id, err := parseID("id", docID)
	if err != nil {
		return nil, err
	}
	if err := do(tid, id); err != nil {
		return nil, err
	}
	d, err := query.Get(ctx, h.app, tid, id)
	if err != nil {
		return nil, err
	}
	return docProto(d), nil
}
