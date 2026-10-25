// Package model is what the credit-note module speaks: the credit note, how its refund goes and why a request is refused.
package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/env"
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

// CreditNote is a numbered correction of a paid invoice.
type CreditNote struct {
	ID, CustomerID, InvoiceID uuid.UUID
	Number, InvoiceNumber     string
	Currency, Reason          string
	TotalMinor                int64
	CreatedAt                 time.Time
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
