// Package model is the vocabulary of e-invoice documents (the einvoice_documents table): what kind of source they have and in which state they are.
package model

// Document kinds and states.
const (
	KindInvoice    = "invoice"
	KindCreditNote = "credit_note"

	Pending   = "pending"
	Issued    = "issued"
	Failed    = "failed"
	Cancelled = "cancelled"
)
