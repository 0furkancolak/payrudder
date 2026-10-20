package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
)

func creditNoteProto(c *runs.CreditNote, customerExternalID string) *billingv1.CreditNote {
	return &billingv1.CreditNote{
		Id: c.ID.String(), Number: c.Number, InvoiceId: c.InvoiceID.String(), InvoiceNumber: c.InvoiceNumber, CustomerId: c.CustomerID.String(),
		CustomerExternalId: customerExternalID, Currency: c.Currency, TotalMinor: c.TotalMinor, Reason: c.Reason, CreateTime: timestamppb.New(c.CreatedAt),
	}
}

// CreateCreditNote implements billingv1.BillingAdminServiceServer.
func (s *Server) CreateCreditNote(ctx context.Context, req *billingv1.CreateCreditNoteRequest) (*billingv1.CreateCreditNoteResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, invalid("tenant_id must be a UUID")
	}
	iid, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		return nil, invalid("invoice_id must be a UUID")
	}
	if len(req.GetReason()) > 500 {
		return nil, invalid("reason is at most 500 characters")
	}
	cn, err := s.runs.CreateCreditNote(ctx, tid, iid, req.GetAmountMinor(), strings.TrimSpace(req.GetReason()))
	switch {
	case errors.Is(err, runs.ErrInvoiceNotFound):
		return nil, status.Error(codes.NotFound, "invoice not found")
	case errors.Is(err, runs.ErrNotPaid), errors.Is(err, runs.ErrNothingLeft):
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, runs.ErrBadAmount):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case err != nil:
		return nil, status.Error(codes.Internal, "internal error")
	}
	var external string
	_ = s.system.QueryRow(ctx, `SELECT COALESCE(snapshot->>'customer_external_id', '') FROM invoices WHERE tenant_id = $1 AND id = $2`, tid, iid).Scan(&external)
	return &billingv1.CreateCreditNoteResponse{CreditNote: creditNoteProto(cn, external)}, nil
}

//nolint:gosec // a SQL select, not a credential
const creditNoteSelect = `SELECT c.id, c.customer_id, c.invoice_id, c.number, COALESCE(i.number, ''), c.currency, c.total_minor, c.reason, c.created_at,
	COALESCE(i.snapshot->>'customer_external_id', '')
	FROM credit_notes c JOIN invoices i ON i.tenant_id = c.tenant_id AND i.id = c.invoice_id`

func scanCreditNote(row pgx.Row) (*billingv1.CreditNote, error) {
	var c runs.CreditNote
	var external string
	if err := row.Scan(&c.ID, &c.CustomerID, &c.InvoiceID, &c.Number, &c.InvoiceNumber, &c.Currency, &c.TotalMinor, &c.Reason, &c.CreatedAt, &external); err != nil {
		return nil, err
	}
	return creditNoteProto(&c, external), nil
}

// GetCreditNote implements billingv1.BillingServiceServer.
func (s *Server) GetCreditNote(ctx context.Context, req *billingv1.GetCreditNoteRequest) (*billingv1.GetCreditNoteResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetCreditNoteId())
	if err != nil {
		return nil, invalid("credit_note_id must be a UUID")
	}
	var out *billingv1.CreditNote
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		out, err = scanCreditNote(tx.QueryRow(ctx, creditNoteSelect+` WHERE c.id = $1`, id))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "credit note not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.GetCreditNoteResponse{CreditNote: out}, nil
}

// ListCreditNotes implements billingv1.BillingServiceServer: newest first.
func (s *Server) ListCreditNotes(ctx context.Context, req *billingv1.ListCreditNotesRequest) (*billingv1.ListCreditNotesResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	size := req.GetPageSize()
	switch {
	case size == 0:
		size = 50
	case size < 0 || size > 200:
		return nil, invalid("page_size must be 1..200")
	}
	var invoice any
	if v := req.GetInvoiceId(); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, invalid("invoice_id must be a UUID")
		}
		invoice = id
	}
	// The token is the created_at and id of the last note of the previous page.
	after, afterID := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if tok := req.GetPageToken(); tok != "" {
		at, id, ok := strings.Cut(tok, "|")
		var perr error
		if after, perr = time.Parse(time.RFC3339Nano, at); !ok || perr != nil {
			return nil, invalid("page_token is not one this API gave")
		}
		if afterID, perr = uuid.Parse(id); perr != nil {
			return nil, invalid("page_token is not one this API gave")
		}
	}
	resp := &billingv1.ListCreditNotesResponse{}
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, creditNoteSelect+` WHERE ($1::uuid IS NULL OR c.invoice_id = $1) AND ($2 = '' OR i.snapshot->>'customer_external_id' = $2)
			AND (c.created_at, c.id) < ($3, $4) ORDER BY c.created_at DESC, c.id DESC LIMIT $5`,
			invoice, req.GetCustomerExternalId(), after, afterID, size+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCreditNote(rows)
			if err != nil {
				return err
			}
			resp.CreditNotes = append(resp.CreditNotes, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if len(resp.CreditNotes) > int(size) {
		resp.CreditNotes = resp.CreditNotes[:size]
		last := resp.CreditNotes[size-1]
		resp.NextPageToken = last.CreateTime.AsTime().UTC().Format(time.RFC3339Nano) + "|" + last.Id
	}
	return resp, nil
}
