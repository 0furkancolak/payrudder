package store

// The words a ledger row (payment_ledger) and a payment attempt are written in.
const (
	// KindCharge and KindRefund are the kinds of ledger row and of payment attempt that move money.
	KindCharge = "charge"
	KindRefund = "refund"
	// KindDispute is a customer's dispute of a payment, written as the case moves: the status of its row is the outcome in the ledger's words (DisputeStatus).
	KindDispute = "dispute"
	// KindDisputeFee is what the provider took for a dispute, a row of its own, once per case (the provider reference of the case and ":fee"): money that left the seller's balance.
	// It is a fact of what the provider reported, not of what Payrudder charged: no listing that folds charges and refunds reads it.
	KindDisputeFee = "dispute_fee"

	// Statuses of a ledger row.
	StatusSucceeded = "succeeded"
	StatusDeclined  = "declined"
	StatusFailed    = "failed"
	StatusPending   = "pending"

	// Sources of a ledger row: who wrote it.
	SourceCommand = "command"
	SourceWebhook = "webhook"
	SourceManual  = "manual"
	// SourceReconciliation is a fact an operator wrote while resolving a discrepancy (reconciliation.Resolve), with the discrepancy and who in its detail.
	SourceReconciliation = "reconciliation"
)

// Statuses of an e-invoice issuance (einvoice_issuances).
const (
	IssuanceStarted   = "started"
	IssuanceIssued    = "issued"
	IssuanceCancelled = "cancelled"
)

// DisputeStatus is the status of the ledger row of a step of a dispute: pending while the case is open, succeeded when it ended in the seller's favour (or never became a
// chargeback), declined when the cardholder won and the money stays with them.
func DisputeStatus(outcome string) string {
	switch outcome {
	case "won":
		return StatusSucceeded
	case "lost":
		return StatusDeclined
	}
	return StatusPending
}
