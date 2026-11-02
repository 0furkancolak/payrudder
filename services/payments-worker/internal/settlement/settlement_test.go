package settlement

import (
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

var day = 24 * time.Hour

func at(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }

func kinds(fs []store.Finding) map[string]store.Finding {
	out := map[string]store.Finding{}
	for _, f := range fs {
		out[f.Kind+":"+f.ProviderRef] = f
	}
	return out
}

// "Paid" and "settled" are different states: the same successful charge is not reported, reported but not paid out, or settled, by what the lines say; and the
// findings are only the ones the lines can support.
func TestCheckSeparatesPaidFromSettled(t *testing.T) {
	paid := func(ref string, d int, amount int64) store.Charge {
		return store.Charge{InvoiceID: "inv-" + ref, Provider: "stripe", ProviderRef: ref, Currency: "USD", AmountMinor: amount, OccurredAt: at(d)}
	}
	settledAt := at(20)
	lines := []store.SettlementLine{
		{Provider: "stripe", LineRef: "t1", Kind: "payment", PaymentRef: "pi_settled", Currency: "USD", GrossMinor: 1000, FeeMinor: 59, NetMinor: 941, OccurredAt: at(2), SettledAt: &settledAt, Batch: "po_1"},
		{Provider: "stripe", LineRef: "t2", Kind: "payment", PaymentRef: "pi_pending", Currency: "USD", GrossMinor: 500, FeeMinor: 45, NetMinor: 455, OccurredAt: at(10)},
		{Provider: "stripe", LineRef: "t3", Kind: "payment", PaymentRef: "pi_short", Currency: "USD", GrossMinor: 480, FeeMinor: 44, NetMinor: 436, OccurredAt: at(3)},
		{Provider: "stripe", LineRef: "t4", Kind: "payment", PaymentRef: "pi_fee", Currency: "USD", GrossMinor: 10000, FeeMinor: 500, NetMinor: 9500, OccurredAt: at(4)},
		{Provider: "stripe", LineRef: "t5", Kind: "payment", PaymentRef: "pi_fx", Currency: "EUR", GrossMinor: 900, FeeMinor: 0, NetMinor: 900, OccurredAt: at(5)},
		{Provider: "stripe", LineRef: "po_1", Kind: "payout", Currency: "USD", GrossMinor: 941, NetMinor: 941, OccurredAt: at(19), SettledAt: &settledAt, Batch: "po_1"},
	}
	in := Input{
		Now: at(30),
		Charges: []store.Charge{
			paid("pi_settled", 2, 1000), paid("pi_pending", 10, 500), paid("pi_short", 3, 500), paid("pi_fee", 4, 10000), paid("pi_fx", 5, 1000),
			paid("pi_missing", 6, 700), // inside the period the lines cover, long ago, and no line: a finding
			paid("pi_before", 1, 300),  // before the first line: the report does not reach back, no finding
			paid("pi_after", 25, 300),  // after the last payment line: not covered yet, no finding
			{InvoiceID: "other", Provider: "paddle", ProviderRef: "txn_1", Currency: "USD", AmountMinor: 100, OccurredAt: at(6)}, // a provider with no lines at all
		},
		Rules: []store.FeeRule{{Provider: "stripe", Currency: "USD", RateBps: 290, FixedMinor: 30}},
		Lines: lines,
	}
	payments, findings := Check(in)
	state := map[string]string{}
	for _, p := range payments {
		state[p.ProviderRef] = p.State
	}
	for ref, want := range map[string]string{"pi_settled": Settled, "pi_pending": PendingPayout, "pi_missing": NotReported, "pi_before": NotReported, "txn_1": NotReported, "pi_fx": PendingPayout} {
		if state[ref] != want {
			t.Errorf("%s is %q, want %q", ref, state[ref], want)
		}
	}
	got := kinds(findings)
	for _, want := range []string{KindMissing + ":pi_missing", KindMismatch + ":pi_short", KindFee + ":pi_fee"} {
		if _, ok := got[want]; !ok {
			t.Errorf("no finding %s in %+v", want, findings)
		}
	}
	if len(findings) != 3 {
		t.Errorf("findings = %+v, want exactly the three above (a conversion, a report that does not reach back, a provider with no lines and the settled payments are not findings)", findings)
	}
	if f := got[KindMismatch+":pi_short"]; f.LedgerMinor != 500 || f.SettledMinor != 480 {
		t.Errorf("mismatch amounts = %+v", f)
	}
	// 290 bps of 10000 is 290, plus 30 fixed = 320; the provider kept 500. The settled payment: 2.9% of 1000 + 30 = 59, as kept, so no finding.
	if f := got[KindFee+":pi_fee"]; f.LedgerMinor != 320 || f.SettledMinor != 500 {
		t.Errorf("fee amounts = %+v", f)
	}
	for _, p := range payments {
		if p.ProviderRef == "pi_settled" && (p.Batch != "po_1" || p.SettledAt == nil || p.ExpectedFeeMinor == nil || *p.ExpectedFeeMinor != 59) {
			t.Errorf("settled payment = %+v", p)
		}
	}
}

// A payment inside the lag is not yet late, even when the lines cover its time.
func TestCheckWaitsForTheLag(t *testing.T) {
	in := Input{Now: at(8), Lag: 7 * day,
		Charges: []store.Charge{{InvoiceID: "a", Provider: "stripe", ProviderRef: "pi_new", Currency: "USD", AmountMinor: 100, OccurredAt: at(6)}},
		Lines: []store.SettlementLine{
			{Provider: "stripe", LineRef: "t1", Kind: "payment", PaymentRef: "pi_a", Currency: "USD", GrossMinor: 1, NetMinor: 1, OccurredAt: at(1)},
			{Provider: "stripe", LineRef: "t2", Kind: "payment", PaymentRef: "pi_b", Currency: "USD", GrossMinor: 1, NetMinor: 1, OccurredAt: at(7)}}}
	if _, f := Check(in); len(f) != 0 {
		t.Fatalf("findings = %+v, want none inside the lag", f)
	}
	in.Now = at(20)
	if _, f := Check(in); len(f) != 1 || f[0].Kind != KindMissing {
		t.Fatalf("findings = %+v, want one settlement_missing after the lag", f)
	}
}

func TestExpectedFeeRoundsHalfUp(t *testing.T) {
	for _, c := range []struct {
		bps          int32
		fixed, gross int64
		want         int64
	}{{290, 30, 1000, 59}, {290, 0, 50, 1}, {250, 0, 20, 1}, {0, 25, 999, 25}, {100, 0, 49, 0}} {
		if got := ExpectedFee(store.FeeRule{RateBps: c.bps, FixedMinor: c.fixed}, c.gross); got != c.want {
			t.Errorf("ExpectedFee(%d bps + %d, %d) = %d, want %d", c.bps, c.fixed, c.gross, got, c.want)
		}
	}
}

func TestParseCSV(t *testing.T) {
	good := "\xef\xbb\xbfReference,Payment_Ref,Currency,Gross,Fee,Occurred_On,Settled_On,Batch\n" +
		"r1,pi_1,usd,10.50,0.61,2026-10-02,2026-10-09,po_9\n" +
		"r2,pi_2,JPY,1000,,2026-10-03T10:00:00Z,,\n"
	lines, errs := ParseCSV(strings.NewReader(good), "stripe")
	if len(errs) != 0 || len(lines) != 2 {
		t.Fatalf("lines %+v errs %+v", lines, errs)
	}
	if l := lines[0]; l.Provider != "stripe" || l.GrossMinor != 1050 || l.FeeMinor != 61 || l.NetMinor != 989 || l.Currency != "USD" || l.SettledAt == nil || l.Batch != "po_9" || l.Source != "csv" || l.Kind != "payment" {
		t.Errorf("line 1 = %+v", l)
	}
	if l := lines[1]; l.GrossMinor != 1000 || l.FeeMinor != 0 || l.SettledAt != nil {
		t.Errorf("line 2 = %+v (JPY has no decimals)", l)
	}

	for name, in := range map[string]string{
		"no reference column":   "currency,gross,occurred_on\nUSD,1,2026-10-01\n",
		"no provider anywhere":  "reference,payment_ref,currency,gross,occurred_on\nr,p,USD,1,2026-10-01\n",
		"a payment with no ref": "provider,reference,currency,gross,occurred_on\nstripe,r,USD,1,2026-10-01\n",
		"too many decimals":     "provider,reference,payment_ref,currency,gross,occurred_on\nstripe,r,p,USD,1.005,2026-10-01\n",
		"a decimal for JPY":     "provider,reference,payment_ref,currency,gross,occurred_on\nstripe,r,p,JPY,1.5,2026-10-01\n",
		"an unknown currency":   "provider,reference,payment_ref,currency,gross,occurred_on\nstripe,r,p,XXX,1,2026-10-01\n",
		"a bad date":            "provider,reference,payment_ref,currency,gross,occurred_on\nstripe,r,p,USD,1,01/10/2026\n",
		"a repeated reference":  "provider,reference,payment_ref,currency,gross,occurred_on\nstripe,r,p,USD,1,2026-10-01\nstripe,r,q,USD,2,2026-10-01\n",
		"net is not gross-fee":  "provider,reference,payment_ref,currency,gross,fee,net,occurred_on\nstripe,r,p,USD,10,1,10,2026-10-01\n",
		"a bad provider":        "provider,reference,payment_ref,currency,gross,occurred_on\nStripe~~eu,r,p,USD,1,2026-10-01\n",
	} {
		if lines, errs := ParseCSV(strings.NewReader(in), ""); len(errs) == 0 || lines != nil {
			t.Errorf("%s: accepted (%+v)", name, lines)
		}
	}
	// A payout needs no payment reference.
	if lines, errs := ParseCSV(strings.NewReader("provider,reference,kind,currency,gross,occurred_on,settled_on,batch\nstripe,po_1,payout,USD,9.41,2026-10-09,2026-10-09,po_1\n"), ""); len(errs) != 0 || len(lines) != 1 || lines[0].Kind != "payout" {
		t.Errorf("payout: %+v %+v", lines, errs)
	}
}
