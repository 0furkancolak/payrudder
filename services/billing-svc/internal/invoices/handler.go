// Package invoices serves the invoice reads of BillingService: it maps requests to queries and results to protos.
package invoices

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/payments"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Handler implements the invoice methods of billingv1.BillingServiceServer.
type Handler struct {
	app  *pgxpool.Pool
	pdfs query.PDFSigner
}

// New returns the handler over the billing_app pool.
func New(app *pgxpool.Pool) *Handler { return &Handler{app: app} }

// WithPDFs enables GetInvoicePdfUrl.
func (h *Handler) WithPDFs(p query.PDFSigner) *Handler {
	h.pdfs = p
	return h
}

// GetInvoicePdfUrl implements billingv1.BillingServiceServer.
func (h *Handler) GetInvoicePdfUrl(ctx context.Context, req *billingv1.GetInvoicePdfUrlRequest) (*billingv1.GetInvoicePdfUrlResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		return nil, errkey.New(codes.InvalidArgument, "billing.invoice.bad_id", "invoice_id must be a UUID")
	}
	url, expires, err := query.PDFURL(ctx, h.app, h.pdfs, ti.ID, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetInvoicePdfUrlResponse{Url: url, ExpireTime: timestamppb.New(expires)}, nil
}

// GetInvoice implements billingv1.BillingServiceServer.
func (h *Handler) GetInvoice(ctx context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.GetInvoiceResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		return nil, errkey.New(codes.InvalidArgument, "billing.invoice.bad_id", "invoice_id must be a UUID")
	}
	inv, err := query.Get(ctx, h.app, ti.ID, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetInvoiceResponse{Invoice: Proto(inv)}, nil
}

// ListInvoices implements billingv1.BillingServiceServer.
func (h *Handler) ListInvoices(ctx context.Context, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	page, err := query.List(ctx, h.app, ti.ID, query.ListParams{
		PageSize: int(req.GetPageSize()), CustomerID: req.GetCustomerId(), PeriodID: req.GetPeriodId(), Status: req.GetStatus(), PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, err
	}
	resp := &billingv1.ListInvoicesResponse{NextPageToken: page.NextPageToken}
	for _, r := range page.Invoices {
		resp.Invoices = append(resp.Invoices, invoiceProto(sqlc.GetInvoiceRow(r)))
	}
	return resp, nil
}

// Proto maps an invoice with its lines to the API message. Other modules that return invoices use it.
func Proto(inv *query.Invoice) *billingv1.Invoice {
	out := invoiceProto(inv.Row)
	for _, l := range inv.Lines {
		out.Lines = append(out.Lines, lineProto(l))
	}
	for _, p := range inv.Payments {
		out.Payments = append(out.Payments, payments.Proto(p))
	}
	return out
}

func invoiceProto(r sqlc.GetInvoiceRow) *billingv1.Invoice {
	inv := &billingv1.Invoice{
		Id: r.ID.String(), CustomerId: r.CustomerID.String(), CustomerExternalId: r.CustomerExternalID, PeriodId: r.PeriodID, Kind: r.Kind, Number: r.Number,
		PlanVersionId: planVersion(r.PlanVersionID), Currency: r.Currency, SubtotalMinor: r.SubtotalMinor, TotalMinor: r.TotalMinor, Status: r.Status,
		SagaState: r.SagaState, PdfObjectKey: r.PdfObjectKey, CreateTime: timestamppb.New(r.CreatedAt),
	}
	if r.FinalizedAt != nil {
		inv.FinalizeTime = timestamppb.New(*r.FinalizedAt)
	}
	return inv
}

// planVersion is empty for an invoice that has no plan: the nil id of a payment-link invoice.
func planVersion(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func lineProto(r sqlc.ListInvoiceLinesRow) *billingv1.InvoiceLine {
	l := &billingv1.InvoiceLine{LineNo: r.LineNo, Kind: r.Kind, MeterCode: r.MeterCode, OriginPeriodId: r.OriginPeriodID, AmountMinor: r.AmountMinor, Description: r.Description}
	if r.ServiceFrom != nil {
		l.ServiceFrom = timestamppb.New(*r.ServiceFrom)
	}
	if r.ServiceTo != nil {
		l.ServiceTo = timestamppb.New(*r.ServiceTo)
	}
	if r.Quantity != "" && r.QuantityScale != nil {
		var units int64
		if err := json.Unmarshal([]byte(r.Quantity), &units); err == nil {
			l.Quantity = quantity.FormatUnits(units, int(*r.QuantityScale))
		}
	}
	return l
}
