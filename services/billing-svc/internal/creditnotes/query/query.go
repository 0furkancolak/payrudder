// Package query reads credit notes. It never writes.
package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
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
	size, ok := page.Size(p.PageSize, 50, 200)
	if !ok {
		return nil, errkey.New(codes.InvalidArgument, "billing.credit_note.bad_page_size", "page_size must be 1..200")
	}
	var invoice *uuid.UUID
	if p.InvoiceID != "" {
		id, err := errkey.ParseUUID("billing.invoice.bad_id", "invoice_id", p.InvoiceID)
		if err != nil {
			return nil, err
		}
		invoice = &id
	}
	// The token is the created_at and id of the last note of the previous page.
	cur := page.Start()
	if p.PageToken != "" {
		if cur, ok = page.ParseCursor(p.PageToken); !ok {
			return nil, errkey.New(codes.InvalidArgument, "billing.credit_note.bad_page_token", "page_token is not one this API gave")
		}
	}
	var rows []sqlc.ListCreditNotesRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListCreditNotes(ctx, sqlc.ListCreditNotesParams{
			InvoiceID: invoice, CustomerExternalID: store.NilIfEmpty(p.CustomerExternalID), AfterAt: cur.At, AfterID: cur.ID, PageSize: size + 1})
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	out := &Page{}
	var more bool
	if out.CreditNotes, more = page.Trim(rows, int(size)); more {
		last := out.CreditNotes[len(out.CreditNotes)-1]
		out.NextPageToken = page.Cursor{At: last.CreatedAt, ID: last.ID}.Token()
	}
	return out, nil
}

// CustomerExternalID is best effort (empty when the lookup fails): a credit note that was just written is returned even if the lookup fails.
func CustomerExternalID(ctx context.Context, system *pgxpool.Pool, tenantID, invoiceID uuid.UUID) string {
	external, _ := sqlc.New(system).GetInvoiceCustomerExternalID(ctx, sqlc.GetInvoiceCustomerExternalIDParams{TenantID: tenantID, ID: invoiceID})
	return external
}
