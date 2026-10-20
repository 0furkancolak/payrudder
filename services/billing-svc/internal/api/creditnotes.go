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
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

var refundStates = map[string]billingv1.RefundState{
	runs.RefundRequested: billingv1.RefundState_REFUND_STATE_REQUESTED,
	runs.RefundRefunded:  billingv1.RefundState_REFUND_STATE_REFUNDED,
	runs.RefundFailed:    billingv1.RefundState_REFUND_STATE_FAILED,
}

func creditNoteProto(c *runs.CreditNote, customerExternalID string) *billingv1.CreditNote {
	out := &billingv1.CreditNote{
		Id: c.ID.String(), Number: c.Number, InvoiceId: c.InvoiceID.String(), InvoiceNumber: c.InvoiceNumber, CustomerId: c.CustomerID.String(),
		CustomerExternalId: customerExternalID, Currency: c.Currency, TotalMinor: c.TotalMinor, Reason: c.Reason, CreateTime: timestamppb.New(c.CreatedAt),
		Disposition: billingv1.CreditNoteDisposition_CREDIT_NOTE_DISPOSITION_CREDIT, RefundError: c.RefundError,
	}
	if c.Refund {
		out.Disposition, out.RefundState = billingv1.CreditNoteDisposition_CREDIT_NOTE_DISPOSITION_REFUND, refundStates[c.RefundState]
	}
	return out
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
	cn, err := s.runs.CreateCreditNote(ctx, tid, iid, req.GetAmountMinor(), strings.TrimSpace(req.GetReason()), req.GetRefundToCard())
	switch {
	case errors.Is(err, runs.ErrInvoiceNotFound):
		return nil, errkey.New(codes.NotFound, "billing.invoice.not_found", "invoice not found")
	case errors.Is(err, runs.ErrNotPaid):
		return nil, errkey.New(codes.FailedPrecondition, "billing.credit_note.not_paid", err.Error())
	case errors.Is(err, runs.ErrNothingLeft):
		return nil, errkey.New(codes.FailedPrecondition, "billing.credit_note.fully_credited", err.Error())
	case errors.Is(err, runs.ErrNoRefund):
		return nil, errkey.New(codes.FailedPrecondition, "billing.credit_note.nothing_to_refund", err.Error())
	case errors.Is(err, runs.ErrBadAmount):
		return nil, errkey.New(codes.InvalidArgument, "billing.credit_note.bad_amount", err.Error())
	case err != nil:
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.CreateCreditNoteResponse{CreditNote: creditNoteProto(cn, s.customerExternalID(ctx, tid, iid))}, nil
}

// RetryCreditNoteRefund implements billingv1.BillingAdminServiceServer.
func (s *Server) RetryCreditNoteRefund(ctx context.Context, req *billingv1.RetryCreditNoteRefundRequest) (*billingv1.RetryCreditNoteRefundResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, invalid("tenant_id must be a UUID")
	}
	id, err := uuid.Parse(req.GetCreditNoteId())
	if err != nil {
		return nil, invalid("credit_note_id must be a UUID")
	}
	cn, err := s.runs.RetryRefund(ctx, tid, id)
	switch {
	case errors.Is(err, runs.ErrInvoiceNotFound):
		return nil, status.Error(codes.NotFound, "credit note not found")
	case errors.Is(err, runs.ErrRefundNotFailed):
		return nil, errkey.New(codes.FailedPrecondition, "billing.credit_note.refund_not_failed", err.Error())
	case err != nil:
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.RetryCreditNoteRefundResponse{CreditNote: creditNoteProto(cn, s.customerExternalID(ctx, tid, cn.InvoiceID))}, nil
}

// customerExternalID is best effort: a credit note that was just written is returned even if the lookup fails.
func (s *Server) customerExternalID(ctx context.Context, tenantID, invoiceID uuid.UUID) string {
	external, _ := sqlc.New(s.system).GetInvoiceCustomerExternalID(ctx, sqlc.GetInvoiceCustomerExternalIDParams{TenantID: tenantID, ID: invoiceID})
	return external
}

func creditNoteRowProto(r sqlc.GetCreditNoteRow) *billingv1.CreditNote {
	return creditNoteProto(&runs.CreditNote{
		ID: r.ID, CustomerID: r.CustomerID, InvoiceID: r.InvoiceID, Number: r.Number, InvoiceNumber: r.InvoiceNumber, Currency: r.Currency, TotalMinor: r.TotalMinor,
		Reason: r.Reason, CreatedAt: r.CreatedAt, Refund: r.Refund, RefundState: r.RefundState, RefundError: r.RefundError,
	}, r.CustomerExternalID)
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
		row, err := sqlc.New(tx).GetCreditNote(ctx, id)
		if err != nil {
			return err
		}
		out = creditNoteRowProto(row)
		return nil
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
	var invoice *uuid.UUID
	if v := req.GetInvoiceId(); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, invalid("invoice_id must be a UUID")
		}
		invoice = &id
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
	var rows []sqlc.ListCreditNotesRow
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListCreditNotes(ctx, sqlc.ListCreditNotesParams{
			InvoiceID: invoice, CustomerExternalID: store.NilIfEmpty(req.GetCustomerExternalId()), AfterAt: after, AfterID: afterID, PageSize: size + 1})
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	resp := &billingv1.ListCreditNotesResponse{}
	for _, r := range rows {
		resp.CreditNotes = append(resp.CreditNotes, creditNoteRowProto(sqlc.GetCreditNoteRow(r)))
	}
	if len(resp.CreditNotes) > int(size) {
		resp.CreditNotes = resp.CreditNotes[:size]
		last := resp.CreditNotes[size-1]
		resp.NextPageToken = last.CreateTime.AsTime().UTC().Format(time.RFC3339Nano) + "|" + last.Id
	}
	return resp, nil
}
