// Package query reads e-documents. It never writes.
package query

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	invoicequery "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

var errNotFound = errkey.New(codes.NotFound, "einvoice.document.not_found", "e-document not found")

// Get reads one e-document of a tenant.
func Get(ctx context.Context, app *pgxpool.Pool, tenantID, id uuid.UUID) (sqlc.GetEInvoiceDocumentRow, error) {
	var out sqlc.GetEInvoiceDocumentRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = sqlc.New(tx).GetEInvoiceDocument(ctx, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errNotFound
	}
	if err != nil {
		return out, apierr.Internal(ctx, err)
	}
	return out, nil
}

// ListParams filter and page the e-documents of a tenant, newest first. Zero PageSize means the default.
type ListParams struct {
	PageSize                        int32
	InvoiceID, CreditNoteID, Status string
	PageToken                       string
}

// Page is a page of e-documents with the total that match the filter.
type Page struct {
	Documents     []sqlc.ListEInvoiceDocumentsRow
	NextPageToken string
	Total         int64
}

func parseOptionalUUID(field, v string) (*uuid.UUID, error) {
	if v == "" {
		return nil, nil
	}
	id, err := errkey.ParseUUID("billing.einvoice.bad_id", field, v)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// List returns a page of the tenant's e-documents.
func List(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, p ListParams) (*Page, error) {
	size, ok := page.Size(p.PageSize, 50, 200)
	if !ok {
		return nil, errkey.New(codes.InvalidArgument, "billing.einvoice.bad_page_size", "page_size must be 1..200")
	}
	invoice, err := parseOptionalUUID("invoice_id", p.InvoiceID)
	if err != nil {
		return nil, err
	}
	note, err := parseOptionalUUID("credit_note_id", p.CreditNoteID)
	if err != nil {
		return nil, err
	}
	switch p.Status {
	case "", "pending", "issued", "failed", "cancelled":
	default:
		return nil, errkey.New(codes.InvalidArgument, "billing.einvoice.bad_status", "status must be pending, issued, failed or cancelled")
	}
	cur := page.Start()
	if p.PageToken != "" {
		if cur, ok = page.ParseCursor(p.PageToken); !ok {
			return nil, errkey.New(codes.InvalidArgument, "billing.einvoice.bad_page_token", "page_token is not one this API gave")
		}
	}
	var rows []sqlc.ListEInvoiceDocumentsRow
	err = pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListEInvoiceDocuments(ctx, sqlc.ListEInvoiceDocumentsParams{InvoiceID: invoice, CreditNoteID: note, Status: store.NilIfEmpty(p.Status),
			AfterAt: cur.At, AfterID: cur.ID, PageSize: size + 1})
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	out := &Page{}
	if len(rows) > 0 {
		out.Total = rows[0].Count
	}
	var more bool
	if out.Documents, more = page.Trim(rows, int(size)); more {
		last := out.Documents[len(out.Documents)-1]
		out.NextPageToken = page.Cursor{At: last.CreatedAt, ID: last.ID}.Token()
	}
	return out, nil
}

// FileURL presigns the download of a document's UBL XML (kind "xml") or PDF ("pdf").
func FileURL(ctx context.Context, app *pgxpool.Pool, signer invoicequery.PDFSigner, tenantID, id uuid.UUID, kind string) (string, time.Time, error) {
	if kind != "xml" && kind != "pdf" {
		return "", time.Time{}, errkey.New(codes.InvalidArgument, "billing.einvoice.bad_file_kind", "kind must be xml or pdf")
	}
	if signer == nil {
		return "", time.Time{}, errkey.New(codes.Unimplemented, "billing.storage.not_configured", "object storage is not configured")
	}
	var keys sqlc.GetEInvoiceFileKeyRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		keys, err = sqlc.New(tx).GetEInvoiceFileKey(ctx, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, errNotFound
	}
	if err != nil {
		return "", time.Time{}, apierr.Internal(ctx, err)
	}
	key := keys.XmlKey
	if kind == "pdf" {
		key = keys.PdfKey
	}
	if key == nil || *key == "" {
		return "", time.Time{}, errkey.New(codes.FailedPrecondition, "einvoice.file.not_ready", "the file is not available (yet)")
	}
	return invoicequery.Sign(ctx, signer, *key)
}
