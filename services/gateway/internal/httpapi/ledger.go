package httpapi

import (
	"encoding/json"
	"time"
)

// The statuses a person sees on a payment.
const (
	payPaid       = "paid"
	payProcessing = "processing"
	payFailed     = "failed"
	payRefunded   = "refunded"
)

// ledgerEntry is a row of payments-worker's ledger as the gateway reads it (no decline details). The provider reference is read only to tell one payment from
// another; it is not passed on. The hosted pages and the customer payments read the same row as ledgerRow.
type ledgerEntry struct {
	ID          json.Number `json:"id"`
	InvoiceID   string      `json:"invoice_id"`
	Provider    string      `json:"provider"`
	Environment string      `json:"environment"`
	Kind        string      `json:"kind"`
	Status      string      `json:"status"`
	AmountMinor json.Number `json:"amount_minor"`
	Currency    string      `json:"currency"`
	ProviderRef string      `json:"provider_ref"`
	OccurredAt  time.Time   `json:"occurred_at"`
}

// ledgerRow is a ledgerEntry where a list of rows is meant, not a payment.
type ledgerRow = ledgerEntry

func (e ledgerEntry) amount() int64 { n, _ := e.AmountMinor.Int64(); return n }

func (e ledgerEntry) id() int64 { n, _ := e.ID.Int64(); return n }

// paymentOf is one payment (or refund) as a person sees it: the rows the ledger holds for one provider reference folded into one outcome.
type paymentOf struct {
	ledgerEntry
	shown string
}

// failedStatus says a charge or refund row ended without the money moving.
func failedStatus(status string) bool { return status == "declined" || status == "failed" }

// rank orders the statuses of the rows of one payment by how settled they are: succeeded, then failed, then still pending.
func rank(status string) int {
	switch {
	case status == "succeeded":
		return 3
	case failedStatus(status):
		return 2
	default: // pending
		return 1
	}
}

// foldLedger turns ledger rows into payments: a charge that was pending and then succeeded is one paid payment, not two. Rows without a provider
// reference stand alone. Disputes and payouts are not payments.
func foldLedger(rows []ledgerEntry) []paymentOf {
	type key struct{ provider, kind, ref string }
	best := map[key]*ledgerEntry{}
	var order []key
	for i := range rows {
		r := &rows[i]
		if r.Kind != "charge" && r.Kind != "refund" {
			continue
		}
		k := key{r.Provider, r.Kind, r.ProviderRef}
		if r.ProviderRef == "" {
			k.ref = "row:" + r.ID.String()
		}
		cur, seen := best[k]
		if !seen {
			order = append(order, k)
			best[k] = r
			continue
		}
		// The most settled outcome wins; between equals the newer row.
		if rank(r.Status) > rank(cur.Status) || (rank(r.Status) == rank(cur.Status) && r.OccurredAt.After(cur.OccurredAt)) {
			best[k] = r
		}
	}
	out := make([]paymentOf, 0, len(order))
	for _, k := range order {
		r := *best[k]
		p := paymentOf{ledgerEntry: r}
		switch {
		case r.Kind == "refund" && r.Status == "succeeded":
			p.shown = payRefunded
		case r.Kind == "refund":
			p.shown = payProcessing
			if rank(r.Status) == 2 {
				p.shown = payFailed
			}
		case r.Status == "succeeded":
			p.shown = payPaid
		case rank(r.Status) == 2:
			p.shown = payFailed
		default:
			p.shown = payProcessing
		}
		out = append(out, p)
	}
	return out
}
