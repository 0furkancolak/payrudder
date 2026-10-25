// Package command changes credit notes: it creates them and retries failed refunds. The rules live here.
package command

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
)

const maxReasonLen = 500

// Create writes a credit note for a paid invoice, optionally refunding it to the card.
func Create(ctx context.Context, mgr *runs.Manager, tenantID, invoiceID uuid.UUID, amountMinor int64, reason string, refundToCard bool) (*runs.CreditNote, error) {
	if len(reason) > maxReasonLen {
		return nil, errkey.New(codes.InvalidArgument, "billing.credit_note.reason_too_long", "reason is at most 500 characters")
	}
	cn, err := mgr.CreateCreditNote(ctx, tenantID, invoiceID, amountMinor, strings.TrimSpace(reason), refundToCard)
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
	return cn, nil
}

// RetryRefund asks again for the refund of a credit note whose refund failed.
func RetryRefund(ctx context.Context, mgr *runs.Manager, tenantID, creditNoteID uuid.UUID) (*runs.CreditNote, error) {
	cn, err := mgr.RetryRefund(ctx, tenantID, creditNoteID)
	switch {
	case errors.Is(err, runs.ErrInvoiceNotFound):
		return nil, errkey.New(codes.NotFound, "billing.credit_note.not_found", "credit note not found")
	case errors.Is(err, runs.ErrRefundNotFailed):
		return nil, errkey.New(codes.FailedPrecondition, "billing.credit_note.refund_not_failed", err.Error())
	case err != nil:
		return nil, status.Error(codes.Internal, "internal error")
	}
	return cn, nil
}
