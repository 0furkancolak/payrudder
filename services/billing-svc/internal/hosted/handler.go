// Package hosted serves the public invoice links: creating and revoking them (BillingService) and showing the invoice to a visitor
// (HostedInvoiceService). It maps requests to commands and queries.
package hosted

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/hosted/command"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/hosted/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/invoices"
	invoicequery "github.com/0furkancolak/quotify/services/billing-svc/internal/invoices/query"
)

// Handler implements the invoice-link methods of billingv1.BillingServiceServer and billingv1.HostedInvoiceServiceServer.
type Handler struct {
	app    *pgxpool.Pool
	system *pgxpool.Pool
	pdfs   invoicequery.PDFSigner
}

// New returns the handler over the billing_app and billing_system pools.
func New(app, system *pgxpool.Pool) *Handler { return &Handler{app: app, system: system} }

// WithPDFs enables the PDF link of the hosted invoice.
func (h *Handler) WithPDFs(p invoicequery.PDFSigner) *Handler {
	h.pdfs = p
	return h
}

func parseInvoiceID(v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, status.Error(codes.InvalidArgument, "invoice_id must be a UUID")
	}
	return id, nil
}

// CreateInvoiceLink implements billingv1.BillingServiceServer.
func (h *Handler) CreateInvoiceLink(ctx context.Context, req *billingv1.CreateInvoiceLinkRequest) (*billingv1.CreateInvoiceLinkResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseInvoiceID(req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	token, expires, err := command.CreateLink(ctx, h.app, ti.ID, id, req.GetExpiresInDays())
	if err != nil {
		return nil, err
	}
	return &billingv1.CreateInvoiceLinkResponse{Token: token, ExpireTime: timestamppb.New(expires)}, nil
}

// RevokeInvoiceLinks implements billingv1.BillingServiceServer.
func (h *Handler) RevokeInvoiceLinks(ctx context.Context, req *billingv1.RevokeInvoiceLinksRequest) (*billingv1.RevokeInvoiceLinksResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseInvoiceID(req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	n, err := command.RevokeLinks(ctx, h.app, ti.ID, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.RevokeInvoiceLinksResponse{Revoked: int32(n)}, nil //nolint:gosec // a handful of links
}

// GetHostedInvoice implements billingv1.HostedInvoiceServiceServer.
func (h *Handler) GetHostedInvoice(ctx context.Context, req *billingv1.GetHostedInvoiceRequest) (*billingv1.GetHostedInvoiceResponse, error) {
	inv, err := query.Get(ctx, h.system, h.pdfs, req.GetToken())
	if err != nil {
		return nil, err
	}
	resp := &billingv1.GetHostedInvoiceResponse{
		Invoice: invoices.Proto(inv.Invoice), ExpireTime: timestamppb.New(inv.ExpiresAt), SellerName: inv.SellerName, CustomerName: inv.CustomerName,
		CurrencyExponent: inv.CurrencyExponent, SellerAddressLines: inv.SellerAddressLines, CustomerAddressLines: inv.CustomerAddressLines, PdfAvailable: inv.PDFAvailable,
	}
	resp.Invoice.PdfObjectKey = "" // the visitor gets a link, not a storage key
	return resp, nil
}

// GetHostedInvoicePdfUrl implements billingv1.HostedInvoiceServiceServer.
func (h *Handler) GetHostedInvoicePdfUrl(ctx context.Context, req *billingv1.GetHostedInvoicePdfUrlRequest) (*billingv1.GetHostedInvoicePdfUrlResponse, error) {
	url, expires, err := query.PDFURL(ctx, h.system, h.pdfs, req.GetToken())
	if err != nil {
		return nil, err
	}
	return &billingv1.GetHostedInvoicePdfUrlResponse{Url: url, ExpireTime: timestamppb.New(expires)}, nil
}
