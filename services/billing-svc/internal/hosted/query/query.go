// Package query reads what a visitor with an invoice link may see. It never writes.
package query

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/hosted/command"
	invoices "github.com/0furkancolak/quotify/services/billing-svc/internal/invoices/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// errNoLink is what a visitor gets for a token that is unknown, expired or revoked: the three look the same.
var errNoLink = errkey.New(codes.NotFound, "billing.invoice_link.not_found", "this link does not exist or has expired")

// Link is what a token resolves to.
type Link struct {
	TenantID  uuid.UUID
	InvoiceID uuid.UUID
	ExpiresAt time.Time
}

// ResolveLink resolves a token to its tenant and invoice, or a not-found error.
func ResolveLink(ctx context.Context, system *pgxpool.Pool, token string) (*Link, error) {
	if len(token) < 20 || len(token) > 100 {
		return nil, errNoLink
	}
	link, err := sqlc.New(system).FindActiveInvoiceLink(ctx, command.TokenHash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoLink
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &Link{TenantID: link.TenantID, InvoiceID: link.InvoiceID, ExpiresAt: link.ExpiresAt}, nil
}

// Invoice is an invoice as a visitor sees it.
type Invoice struct {
	Invoice              *invoices.Invoice
	ExpiresAt            time.Time
	SellerName           string
	CustomerName         string
	CurrencyExponent     int32
	SellerAddressLines   []string
	CustomerAddressLines []string
	PDFAvailable         bool
}

// Get returns the invoice behind a token. pdfs may be nil (then no PDF is available).
func Get(ctx context.Context, system *pgxpool.Pool, pdfs invoices.PDFSigner, token string) (*Invoice, error) {
	link, err := ResolveLink(ctx, system, token)
	if err != nil {
		return nil, err
	}
	out := &Invoice{ExpiresAt: link.ExpiresAt}
	err = pgxx.InTx(ctx, system, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		if out.Invoice, err = invoices.Load(ctx, tx, link.InvoiceID); err != nil {
			return err
		}
		fields, err := sqlc.New(tx).GetInvoiceHostedFields(ctx, link.InvoiceID)
		out.SellerName, out.CustomerName, out.CurrencyExponent = fields.SellerName, fields.CustomerName, fields.CurrencyExponent
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if out.Invoice.Row.Status == "draft" {
		return nil, errNoLink
	}
	addresses, err := sqlc.New(system).GetInvoiceAddresses(ctx, link.InvoiceID)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if err := errors.Join(json.Unmarshal(addresses.SellerAddress, &out.SellerAddressLines), json.Unmarshal(addresses.CustomerAddress, &out.CustomerAddressLines)); err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	out.PDFAvailable = out.Invoice.Row.PdfObjectKey != "" && pdfs != nil
	return out, nil
}

// PDFURL returns a presigned link to the PDF of the invoice behind a token.
func PDFURL(ctx context.Context, system *pgxpool.Pool, pdfs invoices.PDFSigner, token string) (string, time.Time, error) {
	link, err := ResolveLink(ctx, system, token)
	if err != nil {
		return "", time.Time{}, err
	}
	if pdfs == nil {
		return "", time.Time{}, status.Error(codes.Unimplemented, "object storage is not configured")
	}
	key, err := sqlc.New(system).GetInvoicePDFKey(ctx, link.InvoiceID)
	if err != nil {
		return "", time.Time{}, status.Error(codes.Internal, "internal error")
	}
	if key == nil || *key == "" {
		return "", time.Time{}, errkey.New(codes.FailedPrecondition, "billing.invoice.pdf_not_ready", "the invoice PDF is not rendered yet")
	}
	return invoices.Sign(ctx, pdfs, *key)
}
