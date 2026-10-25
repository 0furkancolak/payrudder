// Package command changes the bank transfer state of an invoice: it confirms a receipt and gives up on a pending transfer. The rules live here.
package command

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/transfers/query"
)

// Billing is what the commands ask of billing-svc.
type Billing interface {
	// InvoiceStatus returns the invoice's status (open, paid, uncollectible, void …). found is false for an invoice billing does not know.
	InvoiceStatus(ctx context.Context, tenant, invoice uuid.UUID) (status string, found bool, err error)
	// Nudge tells the invoice's saga to ask for the payment status now instead of at its next poll.
	Nudge(ctx context.Context, tenant, invoice uuid.UUID) error
}

// Deps is what the commands need.
type Deps struct {
	query.Deps
	// Billing may be nil: the invoice status is then not checked and the saga learns of a receipt at its next poll.
	Billing Billing
}

// Receipt is a transfer to confirm.
type Receipt struct {
	Tenant, Invoice uuid.UUID
	AmountMinor     int64
	Currency        string
	// ReceivedAt is when the bank booked it; zero is now.
	ReceivedAt    time.Time
	BankReference string
	Note          string
	Actor         string
}

// Confirmation is the outcome of Confirm.
type Confirmation struct {
	Receipt   store.Receipt
	Duplicate bool
	// Paid: the receipts now reach the total.
	Paid bool
	// SagaNotified: the invoice's saga was told. False when billing-svc could not be reached; the saga then finds the payment at its next poll.
	SagaNotified bool
	State        query.State
}

const (
	maxReferenceLen = 128
	maxNoteLen      = 500
	clockSkew       = 5 * time.Minute
)

// Confirm records money that arrived by bank transfer for an invoice charged through the tenant's bank transfer account. It is idempotent per
// (invoice, bank reference). Partial receipts are recorded and leave the invoice unpaid; when the receipts add up to the total the invoice saga is told.
// A receipt that would take the invoice past its total is refused.
func Confirm(ctx context.Context, d Deps, in Receipt) (Confirmation, error) {
	ref := strings.TrimSpace(in.BankReference)
	switch {
	case ref == "" || utf8.RuneCountInString(ref) > maxReferenceLen || strings.ContainsAny(ref, "\n\r\t\x00"):
		return Confirmation{}, errkey.New(codes.InvalidArgument, "payments.transfer.bad_reference", "bank_reference is required: one line of at most 128 characters")
	case in.AmountMinor <= 0:
		return Confirmation{}, errkey.New(codes.InvalidArgument, "payments.transfer.bad_amount", "amount_minor must be positive")
	case utf8.RuneCountInString(in.Note) > maxNoteLen:
		return Confirmation{}, errkey.New(codes.InvalidArgument, "payments.transfer.bad_note", "note is at most 500 characters")
	}
	now := d.Clock()
	if in.ReceivedAt.IsZero() {
		in.ReceivedAt = now
	}
	if in.ReceivedAt.After(now.Add(clockSkew)) {
		return Confirmation{}, errkey.New(codes.InvalidArgument, "payments.transfer.bad_date", "received_at is in the future")
	}
	st, err := query.Get(ctx, d.Deps, in.Tenant, in.Invoice)
	if err != nil {
		return Confirmation{}, err
	}
	if st.State == query.None {
		return Confirmation{}, errkey.New(codes.FailedPrecondition, "payments.transfer.not_a_transfer",
			"the invoice was not charged by bank transfer; record the payment on the invoice instead")
	}
	if strings.ToUpper(strings.TrimSpace(in.Currency)) != st.Currency {
		return Confirmation{}, errkey.Newf(codes.InvalidArgument, "payments.transfer.bad_currency", "the invoice is in %s", st.Currency)
	}
	if err := d.checkOpen(ctx, in.Tenant, in.Invoice); err != nil {
		return Confirmation{}, err
	}
	res, err := d.Transfers.Record(ctx, store.NewReceipt{TenantID: in.Tenant, InvoiceID: in.Invoice, Provider: banktransfer.Name, Environment: st.Charge.Environment,
		AmountMinor: in.AmountMinor, Currency: st.Currency, BankReference: ref, ReceivedAt: in.ReceivedAt.UTC(), Note: strings.TrimSpace(in.Note), RecordedBy: actor(in.Actor)}, st.TotalMinor)
	var over store.ErrOverpayment
	switch {
	case errors.Is(err, store.ErrReceiptAmount):
		return Confirmation{}, errkey.New(codes.FailedPrecondition, "payments.transfer.reference_reused", "that bank reference was confirmed before with another amount")
	case errors.As(err, &over):
		return Confirmation{}, errkey.Newf(codes.FailedPrecondition, "payments.transfer.overpayment",
			"%d %s is more than is outstanding (%d %s); record the outstanding amount and settle the rest with the customer outside Payrudder (minor units)",
			in.AmountMinor, st.Currency, over.Outstanding, st.Currency)
	case err != nil:
		return Confirmation{}, err
	}
	out := Confirmation{Receipt: res.Receipt, Duplicate: res.Duplicate, Paid: res.Received >= st.TotalMinor, SagaNotified: true}
	if out.Paid {
		out.SagaNotified = d.nudge(ctx, in.Tenant, in.Invoice)
	}
	if out.State, err = query.Get(ctx, d.Deps, in.Tenant, in.Invoice); err != nil {
		return Confirmation{}, err
	}
	return out, nil
}

// Close is the decision to give up on a pending transfer.
type Close struct {
	Tenant, Invoice uuid.UUID
	// Outcome is "rejected" (the money was refused or will not come) or "expired" (the time is up); empty is rejected.
	Outcome string
	Reason  string
	Actor   string
}

// Reject gives up on the pending bank transfer of an invoice: the charge is declined, so the tenant's dunning applies at once. Receipts already confirmed
// stay. Closing a transfer that is closed already changes nothing.
func Reject(ctx context.Context, d Deps, in Close) (query.State, error) {
	code := handler.TransferRejected
	switch in.Outcome {
	case "", "rejected":
	case "expired":
		code = handler.TransferExpired
	default:
		return query.State{}, errkey.New(codes.InvalidArgument, "payments.transfer.bad_outcome", "outcome is rejected or expired")
	}
	if utf8.RuneCountInString(in.Reason) > maxNoteLen {
		return query.State{}, errkey.New(codes.InvalidArgument, "payments.transfer.bad_note", "reason is at most 500 characters")
	}
	st, err := query.Get(ctx, d.Deps, in.Tenant, in.Invoice)
	if err != nil {
		return query.State{}, err
	}
	switch st.State {
	case query.None:
		return query.State{}, errkey.New(codes.FailedPrecondition, "payments.transfer.not_a_transfer", "the invoice was not charged by bank transfer")
	case query.Paid:
		return query.State{}, errkey.New(codes.FailedPrecondition, "payments.transfer.nothing_pending", "the transfer is paid in full")
	case query.Rejected, query.Expired:
		if st.ClosedReason != "" {
			return st, nil // closed before: idempotent
		}
	}
	if err := d.checkOpen(ctx, in.Tenant, in.Invoice); err != nil {
		return query.State{}, err
	}
	detail := code + ": " + strings.TrimSpace(in.Reason) + " (by " + actor(in.Actor) + ")"
	if _, err := d.Transfers.Close(ctx, store.ClosureEntry{TenantID: in.Tenant, InvoiceID: in.Invoice, Provider: banktransfer.Name, Environment: st.Charge.Environment,
		AmountMinor: st.Charge.AmountMinor, Currency: st.Charge.Currency, IdempotencyKey: st.Charge.IdempotencyKey, Source: "manual", Detail: detail}); err != nil {
		return query.State{}, err
	}
	d.nudge(ctx, in.Tenant, in.Invoice)
	if st, err = query.Get(ctx, d.Deps, in.Tenant, in.Invoice); err != nil {
		return query.State{}, err
	}
	return st, nil
}

// checkOpen refuses an invoice that is paid, void or uncollectible. Without a billing client the check is skipped.
func (d Deps) checkOpen(ctx context.Context, tenant, invoice uuid.UUID) error {
	if d.Billing == nil {
		return nil
	}
	st, found, err := d.Billing.InvoiceStatus(ctx, tenant, invoice)
	switch {
	case err != nil:
		return errkey.New(codes.Unavailable, "payments.transfer.unavailable", "billing-svc could not be asked about the invoice; try again")
	case !found:
		return errkey.New(codes.NotFound, "payments.transfer.invoice_not_found", "no such invoice")
	case st == "open":
		return nil
	case st == "uncollectible":
		return errkey.New(codes.FailedPrecondition, "payments.transfer.invoice_closed",
			"the invoice is uncollectible; record the payment on the invoice instead, which settles it")
	}
	return errkey.Newf(codes.FailedPrecondition, "payments.transfer.invoice_closed", "the invoice is %s", st)
}

// nudge tells the saga to ask for the payment status now. It reports whether that worked; a saga that is not waiting (already paid, or past the point where
// it asks) answers FailedPrecondition, which is as good as told. A failure is not an error: the receipt is recorded, and the saga's poll finds it.
func (d Deps) nudge(ctx context.Context, tenant, invoice uuid.UUID) bool {
	if d.Billing == nil {
		return false
	}
	err := d.Billing.Nudge(ctx, tenant, invoice)
	return err == nil || status.Code(err) == codes.FailedPrecondition
}

func actor(a string) string {
	if a = strings.TrimSpace(a); a == "" {
		return "operator"
	}
	return a
}
