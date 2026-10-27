// Package query reads invoices for BillingService. It never writes.
package query

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	paymentquery "github.com/0furkancolak/payrudder/services/billing-svc/internal/payments/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// PDFSigner presigns downloads of invoice PDFs (pkg/blob.Store).
type PDFSigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// PDFLinkTTL is how long a presigned PDF link works.
const PDFLinkTTL = 5 * time.Minute

// Invoice is an invoice with its lines and the payments an operator recorded for it.
type Invoice struct {
	Row      sqlc.GetInvoiceRow
	Lines    []sqlc.ListInvoiceLinesRow
	Payments []sqlc.ListInvoicePaymentsRow
}

// Load reads an invoice with its lines in tx (tenant-scoped, or the system's with the invoice id already known).
// pgx.ErrNoRows is returned as is.
func Load(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Invoice, error) {
	q := sqlc.New(tx)
	row, err := q.GetInvoice(ctx, id)
	if err != nil {
		return nil, err
	}
	lines, err := q.ListInvoiceLines(ctx, id)
	if err != nil {
		return nil, err
	}
	payments, err := paymentquery.ForInvoice(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return &Invoice{Row: row, Lines: lines, Payments: payments}, nil
}

// Get reads one invoice of a tenant.
func Get(ctx context.Context, app pgxx.Beginner, tenantID, id uuid.UUID) (*Invoice, error) {
	var inv *Invoice
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		inv, err = Load(ctx, tx, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.NotFound, "billing.invoice.not_found", "invoice not found")
	}
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return inv, nil
}

// ListParams filter and page the invoices of a tenant. Zero PageSize means the default.
type ListParams struct {
	PageSize   int
	CustomerID string
	PeriodID   string
	Status     string
	PageToken  string
}

// Page is a page of invoices; NextPageToken is empty on the last one.
type Page struct {
	Invoices      []sqlc.ListInvoicesRow
	NextPageToken string
}

// List returns a page of the tenant's invoices.
func List(ctx context.Context, app pgxx.Beginner, tenantID uuid.UUID, p ListParams) (*Page, error) {
	size32, ok := page.Size(int32(p.PageSize), 100, 500) //nolint:gosec // a request's page size
	if !ok {
		return nil, errkey.New(codes.InvalidArgument, "billing.invoice.bad_page_size", "page_size must be 1..500")
	}
	size := int(size32)
	var customer *uuid.UUID
	if p.CustomerID != "" {
		id, err := errkey.ParseUUID("billing.invoice.bad_customer_id", "customer_id", p.CustomerID)
		if err != nil {
			return nil, err
		}
		customer = &id
	}
	after := uuid.Nil
	if p.PageToken != "" {
		var err error
		if after, err = uuid.Parse(p.PageToken); err != nil {
			return nil, errkey.New(codes.InvalidArgument, "billing.invoice.bad_page_token", "page_token is invalid")
		}
	}
	var rows []sqlc.ListInvoicesRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListInvoices(ctx, sqlc.ListInvoicesParams{
			CustomerID: customer, PeriodID: store.NilIfEmpty(p.PeriodID), Status: store.NilIfEmpty(p.Status),
			After: after, PageSize: size32 + 1,
		})
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	out := &Page{}
	var more bool
	if out.Invoices, more = page.Trim(rows, size); more {
		out.NextPageToken = out.Invoices[len(out.Invoices)-1].ID.String()
	}
	return out, nil
}

// PDFURL returns a presigned link to the PDF of a tenant's invoice.
func PDFURL(ctx context.Context, app pgxx.Beginner, pdfs PDFSigner, tenantID, id uuid.UUID) (url string, expires time.Time, err error) {
	if pdfs == nil {
		return "", time.Time{}, errkey.New(codes.Unimplemented, "billing.storage.not_configured", "object storage is not configured")
	}
	var key string
	err = pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		k, err := sqlc.New(tx).GetInvoicePDFKey(ctx, id)
		if err != nil {
			return err
		}
		if k != nil {
			key = *k
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, errkey.New(codes.NotFound, "billing.invoice.not_found", "invoice not found")
	}
	if err != nil {
		return "", time.Time{}, apierr.Internal(ctx, err)
	}
	if key == "" {
		return "", time.Time{}, errkey.New(codes.FailedPrecondition, "billing.invoice.pdf_not_ready", "the invoice PDF is not rendered yet")
	}
	return Sign(ctx, pdfs, key)
}

// Sign presigns the download of the PDF stored under key.
func Sign(ctx context.Context, pdfs PDFSigner, key string) (string, time.Time, error) {
	url, err := pdfs.PresignGet(ctx, key, PDFLinkTTL)
	if err != nil {
		return "", time.Time{}, errkey.New(codes.Unavailable, "billing.invoice.pdf_sign_failed", "could not sign the download link")
	}
	return url, time.Now().Add(PDFLinkTTL), nil
}
