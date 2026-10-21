// Package query reads invoices for BillingService. It never writes.
package query

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/pgxx"
	paymentquery "github.com/0furkancolak/quotify/services/billing-svc/internal/payments/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
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
func Get(ctx context.Context, app *pgxpool.Pool, tenantID, id uuid.UUID) (*Invoice, error) {
	var inv *Invoice
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		inv, err = Load(ctx, tx, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "invoice not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
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
func List(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, p ListParams) (*Page, error) {
	size := p.PageSize
	if size <= 0 {
		size = 100
	}
	if size > 500 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be 1..500")
	}
	var customer *uuid.UUID
	if p.CustomerID != "" {
		id, err := uuid.Parse(p.CustomerID)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "customer_id must be a UUID")
		}
		customer = &id
	}
	after := uuid.Nil
	if p.PageToken != "" {
		var err error
		if after, err = uuid.Parse(p.PageToken); err != nil {
			return nil, status.Error(codes.InvalidArgument, "page_token is invalid")
		}
	}
	var rows []sqlc.ListInvoicesRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListInvoices(ctx, sqlc.ListInvoicesParams{
			CustomerID: customer, PeriodID: store.NilIfEmpty(p.PeriodID), Status: store.NilIfEmpty(p.Status),
			After: after, PageSize: int32(size + 1), //nolint:gosec // at most 501
		})
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	out := &Page{}
	if len(rows) > size {
		out.NextPageToken = rows[size-1].ID.String()
		rows = rows[:size]
	}
	out.Invoices = rows
	return out, nil
}

// PDFURL returns a presigned link to the PDF of a tenant's invoice.
func PDFURL(ctx context.Context, app *pgxpool.Pool, pdfs PDFSigner, tenantID, id uuid.UUID) (url string, expires time.Time, err error) {
	if pdfs == nil {
		return "", time.Time{}, status.Error(codes.Unimplemented, "object storage is not configured")
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
		return "", time.Time{}, status.Error(codes.NotFound, "invoice not found")
	}
	if err != nil {
		return "", time.Time{}, status.Error(codes.Internal, "internal error")
	}
	if key == "" {
		return "", time.Time{}, status.Error(codes.FailedPrecondition, "the invoice PDF is not rendered yet")
	}
	return Sign(ctx, pdfs, key)
}

// Sign presigns the download of the PDF stored under key.
func Sign(ctx context.Context, pdfs PDFSigner, key string) (string, time.Time, error) {
	url, err := pdfs.PresignGet(ctx, key, PDFLinkTTL)
	if err != nil {
		return "", time.Time{}, status.Error(codes.Unavailable, "could not sign the download link")
	}
	return url, time.Now().Add(PDFLinkTTL), nil
}
