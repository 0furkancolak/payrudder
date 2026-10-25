// Package model is the vocabulary of e-invoice documents (the einvoice_documents table): what kind of source they have and in which state they are.
package model

import (
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// ErrNotFound is the answer for a document that does not exist (for the tenant asking).
var ErrNotFound = errkey.New(codes.NotFound, "einvoice.document.not_found", "e-document not found")

// Enabled reports whether a tenant's e_invoice_on setting asks for e-invoices at all: empty and "off" do not.
func Enabled(on string) bool { return on != "" && on != "off" }

// Document kinds and states.
const (
	KindInvoice    = "invoice"
	KindCreditNote = "credit_note"

	Pending   = "pending"
	Issued    = "issued"
	Failed    = "failed"
	Cancelled = "cancelled"
)
