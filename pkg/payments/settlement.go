package payments

import (
	"context"
	"time"
)

// A payment that succeeded is not money in the bank: the provider settles it later, minus its fee, in a payout. SettlementReader is what an adapter implements when its
// provider documents an API for that, so the reconciliation can tell "paid" from "settled" (docs/plans/22 G7). An adapter whose provider documents none (or whose
// documentation does not settle what it returns) does not implement it; its settlement is imported from the provider's or the bank's report instead.

// SettlementLine is what a provider says about the money of one payment, or of one payout. Amounts are signed minor units of Currency, which is the currency the
// provider settles in and can differ from the one the customer paid in.
type SettlementLine struct {
	// Ref is the provider's id of the line (a balance transaction): reading it again updates the line, it is never a second one.
	Ref string
	// Kind is "payment" (money for one payment) or "payout" (money sent to the bank).
	Kind string
	// PaymentRef is the reference the payment ledger holds for the payment (the charge id of the adapter), empty for a payout.
	PaymentRef string
	// Currency is the upper-case ISO 4217 code.
	Currency string
	// GrossMinor is what the payment came to, FeeMinor what the provider kept, NetMinor what is left: gross - fee.
	GrossMinor, FeeMinor, NetMinor int64
	OccurredAt                     time.Time
	// AvailableOn is when the funds become available to pay out; SettledAt when they were paid out. Nil while unknown.
	AvailableOn, SettledAt *time.Time
	// Batch is the payout (or settlement) the line went out in, empty until it did.
	Batch string
}

// SettlementReader is a provider adapter that can list what it settled since a time.
type SettlementReader interface {
	Provider
	// Settlement returns the lines the provider created or paid out since the given time, payments and payouts. The same line read twice is the same line.
	Settlement(ctx context.Context, since time.Time) ([]SettlementLine, error)
}
