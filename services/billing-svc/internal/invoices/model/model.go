// Package model is the vocabulary of invoices: the status the invoice row carries and the kind that says what made it.
package model

// Status of an invoice (invoices.status). The saga mirrors its outcome here; the saga's own state is sagas/model.
const (
	StatusDraft         = "draft"
	StatusOpen          = "open"
	StatusPaid          = "paid"
	StatusVoid          = "void"
	StatusUncollectible = "uncollectible"
)

// Kind of an invoice (invoices.kind).
const (
	KindRegular    = "REGULAR"    // a billing run's invoice for a period
	KindFee        = "FEE"        // a fixed fee billed in advance, or what a plan change moved
	KindCorrection = "CORRECTION" // what a re-rating job bills on top of a closed period
	KindLink       = "LINK"       // the invoice of one use of an ad-hoc payment link
)
