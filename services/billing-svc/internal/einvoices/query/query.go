// Package query reads e-documents. It never writes.
package query

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	invoicequery "github.com/0furkancolak/quotify/services/billing-svc/internal/invoices/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
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
		return out, status.Error(codes.Internal, "internal error")
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
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, field+" must be a UUID")
	}
	return &id, nil
}

// List returns a page of the tenant's e-documents.
func List(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, p ListParams) (*Page, error) {
	size := p.PageSize
	switch {
	case size == 0:
		size = 50
	case size < 0 || size > 200:
		return nil, status.Error(codes.InvalidArgument, "page_size must be 1..200")
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
		return nil, status.Error(codes.InvalidArgument, "status must be pending, issued, failed or cancelled")
	}
	after, afterID := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if p.PageToken != "" {
		at, id, ok := strings.Cut(p.PageToken, "|")
		var perr error
		if after, perr = time.Parse(time.RFC3339Nano, at); !ok || perr != nil {
			return nil, status.Error(codes.InvalidArgument, "page_token is not one this API gave")
		}
		if afterID, perr = uuid.Parse(id); perr != nil {
			return nil, status.Error(codes.InvalidArgument, "page_token is not one this API gave")
		}
	}
	var rows []sqlc.ListEInvoiceDocumentsRow
	err = pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListEInvoiceDocuments(ctx, sqlc.ListEInvoiceDocumentsParams{InvoiceID: invoice, CreditNoteID: note, Status: store.NilIfEmpty(p.Status),
			AfterAt: after, AfterID: afterID, PageSize: size + 1})
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	out := &Page{Documents: rows}
	if len(rows) > 0 {
		out.Total = rows[0].Count
	}
	if len(rows) > int(size) {
		out.Documents = rows[:size]
		last := out.Documents[size-1]
		out.NextPageToken = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID.String()
	}
	return out, nil
}

// FileURL presigns the download of a document's UBL XML (kind "xml") or PDF ("pdf").
func FileURL(ctx context.Context, app *pgxpool.Pool, signer invoicequery.PDFSigner, tenantID, id uuid.UUID, kind string) (string, time.Time, error) {
	if kind != "xml" && kind != "pdf" {
		return "", time.Time{}, status.Error(codes.InvalidArgument, "kind must be xml or pdf")
	}
	if signer == nil {
		return "", time.Time{}, status.Error(codes.Unimplemented, "object storage is not configured")
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
		return "", time.Time{}, status.Error(codes.Internal, "internal error")
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
