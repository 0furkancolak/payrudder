// Package model is the vocabulary of invoices: the status the invoice row carries and the kind that says what made it.
package model

import "time"

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

// NumberYear is the calendar year the number of an invoice or credit note issued at the given moment belongs to: the year of that moment on the wall clock of zone,
// the tenant's time zone frozen into the invoice's snapshot. An empty zone (a draft made before the zone was frozen) or one that does not load is UTC, which is how
// every number was dated until then. The numbers of a year are a series of their own per tenant (invoice_number_seq, credit_note_seq are keyed by this year), so
// the series stays gap-free and unique whichever zone dates it.
func NumberYear(at time.Time, zone string) int32 {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	return int32(at.In(loc).Year()) //nolint:gosec // a calendar year
}
