// Package settlement tells "paid" from "settled" (docs/plans/22 G7). A payment that succeeded is in the payment ledger the moment the provider says so; the money
// reaches the tenant later, minus the provider's fee, in a payout. What the provider (or the bank) reports about that is kept as settlement lines, read from a
// provider's API where its adapter documents one (payments.SettlementReader) or imported from a report (CSV); Check lays the two side by side. It writes nothing.
package settlement

import (
	"time"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// The states of a payment that succeeded, by what the provider has reported about its money.
const (
	// NotReported: paid, and no settlement line is known for it.
	NotReported = "not_reported"
	// PendingPayout: the provider reported the payment but has not paid it out.
	PendingPayout = "pending_payout"
	// Settled: the provider paid it out (settled_at, in batch).
	Settled = "settled"
)

// The kinds of discrepancy settlement adds to the comparison of billing with the ledger.
const (
	// KindMissing: paid long enough ago, inside the period the provider's lines cover, and no line names it.
	KindMissing = "settlement_missing"
	// KindMismatch: the line's gross is not the amount paid (same currency only: a conversion changes the amount).
	KindMismatch = "settlement_mismatch"
	// KindFee: the provider kept another fee than the tenant's fee rule expects.
	KindFee = "fee_mismatch"
)

// DefaultLag is how long after a payment its settlement is expected to be reported before its absence is a finding (default, not agreed: Stripe documents no
// delay for balance transactions, and the payout schedule is the account's own).
const DefaultLag = 7 * 24 * time.Hour

// FeeTolerance is how many minor units the fee may differ from the expected one before it is a finding: a rule is a rate, and a provider rounds its own way.
const FeeTolerance = 1

// Payment is a successful charge with what is known about its settlement.
type Payment struct {
	InvoiceID   string    `json:"invoice_id"`
	Number      string    `json:"number,omitempty"`
	Provider    string    `json:"provider"`
	ProviderRef string    `json:"provider_ref"`
	Currency    string    `json:"currency"`
	AmountMinor int64     `json:"amount_minor"`
	PaidAt      time.Time `json:"paid_at"`
	State       string    `json:"state"`
	// The rest is known once the provider reported the payment.
	SettlementCurrency string     `json:"settlement_currency,omitempty"`
	GrossMinor         *int64     `json:"gross_minor,omitempty"`
	FeeMinor           *int64     `json:"fee_minor,omitempty"`
	NetMinor           *int64     `json:"net_minor,omitempty"`
	ExpectedFeeMinor   *int64     `json:"expected_fee_minor,omitempty"`
	AvailableOn        *time.Time `json:"available_on,omitempty"`
	SettledAt          *time.Time `json:"settled_at,omitempty"`
	Batch              string     `json:"settlement_batch,omitempty"`
}

// Input is what Check compares: the successful charges of a period's paid invoices, the settlement lines read, and the tenant's fee rules.
type Input struct {
	Now     time.Time
	Lag     time.Duration
	Charges []store.Charge
	// Numbers maps an invoice id to its printed number, for the findings.
	Numbers map[string]string
	Lines   []store.SettlementLine
	Rules   []store.FeeRule
}

// ExpectedFee is what a rule says the provider keeps of gross: gross * rate_bps / 10000, rounded half up, plus the fixed part.
func ExpectedFee(r store.FeeRule, gross int64) int64 {
	return (gross*int64(r.RateBps)+5000)/10000 + r.FixedMinor
}

type key struct{ provider, ref string }

// Check puts every charge beside its settlement line, and returns the findings. A charge a provider has not reported is a finding only when it was paid more than
// Lag ago and the provider's lines cover its time (the first and last payment line read bracket the period the report speaks for), because absence from a report
// that never reached back that far says nothing.
func Check(in Input) ([]Payment, []store.Finding) {
	lag := in.Lag
	if lag <= 0 {
		lag = DefaultLag
	}
	lines := map[key]store.SettlementLine{}
	first, last := map[string]time.Time{}, map[string]time.Time{}
	for _, l := range in.Lines {
		if l.Kind != "payment" {
			continue
		}
		lines[key{l.Provider, l.PaymentRef}] = l
		if f, ok := first[l.Provider]; !ok || l.OccurredAt.Before(f) {
			first[l.Provider] = l.OccurredAt
		}
		if l.OccurredAt.After(last[l.Provider]) {
			last[l.Provider] = l.OccurredAt
		}
	}
	rules := map[key]store.FeeRule{}
	for _, r := range in.Rules {
		rules[key{r.Provider, r.Currency}] = r
	}

	var payments []Payment
	var findings []store.Finding
	for _, c := range in.Charges {
		p := Payment{InvoiceID: c.InvoiceID, Number: in.Numbers[c.InvoiceID], Provider: c.Provider, ProviderRef: c.ProviderRef, Currency: c.Currency, AmountMinor: c.AmountMinor,
			PaidAt: c.OccurredAt, State: NotReported}
		finding := func(kind, detail string, ledger, settled int64) {
			findings = append(findings, store.Finding{Kind: kind, InvoiceID: c.InvoiceID, InvoiceNumber: p.Number, Provider: c.Provider, ProviderRef: c.ProviderRef, Currency: c.Currency,
				Detail: detail, LedgerMinor: ledger, SettledMinor: settled})
		}
		l, ok := lines[key{c.Provider, c.ProviderRef}]
		if !ok {
			if in.Now.Sub(c.OccurredAt) >= lag && !c.OccurredAt.Before(first[c.Provider]) && !c.OccurredAt.After(last[c.Provider]) && !first[c.Provider].IsZero() {
				finding(KindMissing, "paid on "+c.OccurredAt.UTC().Format("2006-01-02")+" and not in the settlement lines of "+c.Provider+", which cover "+
					first[c.Provider].UTC().Format("2006-01-02")+" to "+last[c.Provider].UTC().Format("2006-01-02"), c.AmountMinor, 0)
			}
			payments = append(payments, p)
			continue
		}
		gross, fee, net := l.GrossMinor, l.FeeMinor, l.NetMinor
		p.State, p.SettlementCurrency, p.GrossMinor, p.FeeMinor, p.NetMinor = PendingPayout, l.Currency, &gross, &fee, &net
		p.AvailableOn, p.SettledAt, p.Batch = l.AvailableOn, l.SettledAt, l.Batch
		if l.SettledAt != nil {
			p.State = Settled
		}
		if l.Currency == c.Currency && l.GrossMinor != c.AmountMinor {
			finding(KindMismatch, "the ledger holds "+c.Currency+" amount_minor "+itoa(c.AmountMinor)+", the settlement line gross is "+itoa(l.GrossMinor), c.AmountMinor, l.GrossMinor)
		}
		if r, ok := rules[key{c.Provider, l.Currency}]; ok {
			want := ExpectedFee(r, l.GrossMinor)
			p.ExpectedFeeMinor = &want
			if d := l.FeeMinor - want; d > FeeTolerance || d < -FeeTolerance {
				finding(KindFee, "the fee rule expects "+itoa(want)+", the provider kept "+itoa(l.FeeMinor), want, l.FeeMinor)
			}
		}
		payments = append(payments, p)
	}
	return payments, findings
}

func itoa(n int64) string {
	return formatInt(n)
}
