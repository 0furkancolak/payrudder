package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// GetLink reads one payment link of a tenant.
func GetLink(ctx context.Context, app *pgxpool.Pool, tenantID, id uuid.UUID) (sqlc.GetPaymentLinkRowRow, error) {
	var row sqlc.GetPaymentLinkRowRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		row, err = sqlc.New(tx).GetPaymentLinkRow(ctx, sqlc.GetPaymentLinkRowParams{TenantID: tenantID, ID: id})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errkey.New(codes.NotFound, "billing.payment_link.not_found", "no such payment link")
	}
	if err != nil {
		return row, apierr.Internal(ctx, err)
	}
	return row, nil
}

// ListParams filter and page the payment links of a tenant. Zero PageSize means the default.
type ListParams struct {
	Kind               string
	Status             string
	InvoiceID          string
	CustomerExternalID string
	PageSize           int
	PageToken          string
}

// Page is a page of payment links; NextPageToken is empty on the last one.
type LinkPage struct {
	Links         []sqlc.ListPaymentLinksRow
	NextPageToken string
}

// ListLinks returns a page of the tenant's payment links, newest first.
func ListLinks(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, p ListParams) (*LinkPage, error) {
	size32, ok := page.Size(int32(p.PageSize), 50, 200) //nolint:gosec // a request's page size
	if !ok {
		return nil, errkey.New(codes.InvalidArgument, "billing.payment_link.bad_page", "page_size must be 1..200")
	}
	size := int(size32)
	if p.Kind != "" && p.Kind != "invoice" && p.Kind != "adhoc" && p.Kind != "credit_pack" {
		return nil, errkey.New(codes.InvalidArgument, "billing.payment_link.bad_kind", "kind must be invoice, adhoc or credit_pack")
	}
	switch p.Status {
	case "", "active", "paid", "expired", "revoked":
	default:
		return nil, errkey.New(codes.InvalidArgument, "billing.payment_link.bad_status", "status must be active, paid, expired or revoked")
	}
	var invoice *uuid.UUID
	if p.InvoiceID != "" {
		id, err := errkey.ParseUUID("billing.invoice.bad_id", "invoice_id", p.InvoiceID)
		if err != nil {
			return nil, err
		}
		invoice = &id
	}
	// The token is the created_at and id of the last link of the previous page.
	cur := page.Start()
	if p.PageToken != "" {
		if cur, ok = page.ParseCursor(p.PageToken); !ok {
			return nil, errkey.New(codes.InvalidArgument, "billing.invoice_link.bad_page_token", "page_token is not one this API gave")
		}
	}
	var rows []sqlc.ListPaymentLinksRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListPaymentLinks(ctx, sqlc.ListPaymentLinksParams{
			TenantID: tenantID, Kind: store.NilIfEmpty(p.Kind), Status: store.NilIfEmpty(p.Status), InvoiceID: invoice, CustomerExternalID: store.NilIfEmpty(p.CustomerExternalID),
			AfterAt: cur.At, AfterID: cur.ID, PageSize: size32 + 1, //nolint:gosec // at most 201
		})
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	out := &LinkPage{}
	var more bool
	if out.Links, more = page.Trim(rows, size); more {
		last := out.Links[len(out.Links)-1]
		out.NextPageToken = page.Cursor{At: last.CreatedAt, ID: last.ID}.Token()
	}
	return out, nil
}
