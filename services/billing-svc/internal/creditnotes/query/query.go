// Package query reads credit notes. It never writes.
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
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Get reads one credit note of a tenant.
func Get(ctx context.Context, app *pgxpool.Pool, tenantID, id uuid.UUID) (sqlc.GetCreditNoteRow, error) {
	var out sqlc.GetCreditNoteRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		out, err = sqlc.New(tx).GetCreditNote(ctx, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errkey.New(codes.NotFound, "billing.credit_note.not_found", "credit note not found")
	}
	if err != nil {
		return out, status.Error(codes.Internal, "internal error")
	}
	return out, nil
}

// ListParams filter and page the credit notes of a tenant, newest first. Zero PageSize means the default.
type ListParams struct {
	PageSize           int32
	InvoiceID          string
	CustomerExternalID string
	PageToken          string
}

// Page is a page of credit notes; NextPageToken is empty on the last one.
type Page struct {
	CreditNotes   []sqlc.ListCreditNotesRow
	NextPageToken string
}

// List returns a page of the tenant's credit notes.
func List(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, p ListParams) (*Page, error) {
	size := p.PageSize
	switch {
	case size == 0:
		size = 50
	case size < 0 || size > 200:
		return nil, errkey.New(codes.InvalidArgument, "billing.credit_note.bad_page_size", "page_size must be 1..200")
	}
	var invoice *uuid.UUID
	if p.InvoiceID != "" {
		id, err := uuid.Parse(p.InvoiceID)
		if err != nil {
			return nil, errkey.New(codes.InvalidArgument, "billing.invoice.bad_id", "invoice_id must be a UUID")
		}
		invoice = &id
	}
	// The token is the created_at and id of the last note of the previous page.
	after, afterID := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if p.PageToken != "" {
		at, id, ok := strings.Cut(p.PageToken, "|")
		var perr error
		if after, perr = time.Parse(time.RFC3339Nano, at); !ok || perr != nil {
			return nil, errkey.New(codes.InvalidArgument, "billing.credit_note.bad_page_token", "page_token is not one this API gave")
		}
		if afterID, perr = uuid.Parse(id); perr != nil {
			return nil, errkey.New(codes.InvalidArgument, "billing.credit_note.bad_page_token", "page_token is not one this API gave")
		}
	}
	var rows []sqlc.ListCreditNotesRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListCreditNotes(ctx, sqlc.ListCreditNotesParams{
			InvoiceID: invoice, CustomerExternalID: store.NilIfEmpty(p.CustomerExternalID), AfterAt: after, AfterID: afterID, PageSize: size + 1})
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	out := &Page{CreditNotes: rows}
	if len(rows) > int(size) {
		out.CreditNotes = rows[:size]
		last := out.CreditNotes[size-1]
		out.NextPageToken = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID.String()
	}
	return out, nil
}

// CustomerExternalID is best effort (empty when the lookup fails): a credit note that was just written is returned even if the lookup fails.
func CustomerExternalID(ctx context.Context, system *pgxpool.Pool, tenantID, invoiceID uuid.UUID) string {
	external, _ := sqlc.New(system).GetInvoiceCustomerExternalID(ctx, sqlc.GetInvoiceCustomerExternalIDParams{TenantID: tenantID, ID: invoiceID})
	return external
}
