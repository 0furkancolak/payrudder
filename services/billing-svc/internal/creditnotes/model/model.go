// Package model is what the credit-note module speaks: the credit note, how its refund goes and why a request is refused.
package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/subscription"
)

// Refusals of CreateCreditNote.
var (
	ErrInvoiceNotFound = errors.New("credit note: no such invoice")
	// ErrNoteNotFound: no such credit note (to retry the refund of).
	ErrNoteNotFound = errors.New("credit note: no such credit note")
	// ErrNotPaid: a credit note corrects an invoice the customer has paid. An open invoice is voided instead; a declined one is dunned.
	ErrNotPaid     = errors.New("credit note: only a paid invoice can be credited")
	ErrBadAmount   = errors.New("credit note: the amount must be positive and at most what is left of the invoice")
	ErrNothingLeft = errors.New("credit note: the invoice has been credited in full")
	// ErrNoRefund: the invoice was not paid by a charge a provider can refund.
	ErrNoRefund = errors.New("credit note: the invoice has no card payment to refund")
	// ErrRefundNotFailed: only a refund that failed is retried.
	ErrRefundNotFailed = errors.New("credit note: the refund has not failed")
)

// Refund states of a credit note.
const (
	RefundNone      = "none"
	RefundRequested = "requested"
	RefundRefunded  = "refunded"
	RefundFailed    = "failed"
)

// How an operator resolves a failed refund (ResolveRefund): the customer was paid back, or was not.
const (
	ResolutionRefunded    = "refunded"
	ResolutionNotRefunded = "not_refunded"
)

// CreditNote is a numbered correction of a paid invoice.
type CreditNote struct {
	ID, CustomerID, InvoiceID uuid.UUID
	Number, InvoiceNumber     string
	Currency, Reason          string
	TotalMinor                int64
	// TaxMinor is the part of TotalMinor that is tax given back (TaxShare).
	TaxMinor  int64
	CreatedAt time.Time
	// Refund is true when the amount goes back to the card; RefundState and RefundError then say how that went.
	Refund                   bool
	RefundState, RefundError string
}

// Number formats a credit note number: CN-2026-000007 for live tenants, SBX-CN-2026-000007 for sandbox ones.
func Number(environment string, year int, seq int64) string {
	prefix := "CN"
	if environment == env.Sandbox.String() {
		prefix = env.SandboxNumberPrefix + "CN"
	}
	return fmt.Sprintf("%s-%d-%06d", prefix, year, seq)
}

// Disposition is what the credit note does with its amount: it goes back to the card (refund) or onto the customer's credit balance (credit).
func Disposition(refund bool) string {
	if refund {
		return "refund"
	}
	return "credit"
}

// TaxShare is the tax a credit note of amount gives back (docs/code-quality/billing-svc-status.md, plan 05): the note's share of the invoice's total is its share of
// the invoice's tax, rounded half to even like the tax itself. invoiceTax is the sum of the invoice's TAX lines, invoiceTotal what the customer owed, taxBefore the tax
// the earlier notes of the invoice gave back and left what of the total they left uncredited. The note that credits all that is left gives back all the tax that is left
// (so the notes of an invoice add up to its tax exactly, whatever their sizes and the rounding), and no note gives back more than it credits or than the invoice has
// left. A note made before this rule carries no tax (taxBefore does not count it); the note that closes the invoice then takes everything still unreturned.
func TaxShare(invoiceTax, invoiceTotal, taxBefore, left, amount int64) int64 {
	remaining := invoiceTax - taxBefore
	if invoiceTax <= 0 || invoiceTotal <= 0 || remaining <= 0 {
		return 0
	}
	tax := remaining
	if amount < left {
		tax = min(subscription.ScaleHalfEven(invoiceTax, amount, invoiceTotal), remaining)
	}
	return min(tax, amount)
}
