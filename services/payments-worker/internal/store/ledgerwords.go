package store

// The words a ledger row (payment_ledger) and a payment attempt are written in.
const (
	// KindCharge and KindRefund are the kinds of ledger row and of payment attempt that move money.
	KindCharge = "charge"
	KindRefund = "refund"

	// Statuses of a ledger row.
	StatusSucceeded = "succeeded"
	StatusDeclined  = "declined"
	StatusFailed    = "failed"
	StatusPending   = "pending"

	// Sources of a ledger row: who wrote it.
	SourceCommand = "command"
	SourceWebhook = "webhook"
	SourceManual  = "manual"
)

// Statuses of an e-invoice issuance (einvoice_issuances).
const (
	IssuanceStarted   = "started"
	IssuanceIssued    = "issued"
	IssuanceCancelled = "cancelled"
)
