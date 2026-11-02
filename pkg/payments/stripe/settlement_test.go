package stripe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A fake Stripe that serves balance transactions and payouts the way the documentation describes them: a payment's balance transaction names its charge (expanded,
// with the PaymentIntent), the payouts list holds the paid ones, and the transactions of a payout are listed by payout=po_....
func settlementFake(t *testing.T, calls *[]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.Path+"?"+r.URL.RawQuery)
		q := r.URL.Query()
		charge := func(n string) map[string]any {
			return map[string]any{"id": "ch_" + n, "object": "charge", "payment_intent": "pi_" + n}
		}
		txn := func(id, cat, n string, amount, fee int64) map[string]any {
			return map[string]any{"id": id, "object": "balance_transaction", "reporting_category": cat, "amount": amount, "fee": fee, "net": amount - fee, "currency": "usd",
				"created": 1_700_000_000, "available_on": 1_700_200_000, "source": charge(n)}
		}
		var body map[string]any
		switch {
		case r.URL.Path == "/v1/balance_transactions" && q.Get("payout") == "po_1":
			body = map[string]any{"has_more": false, "data": []any{txn("txn_a", "charge", "a", 1000, 59)}}
		case r.URL.Path == "/v1/balance_transactions" && q.Get("starting_after") == "txn_r":
			body = map[string]any{"has_more": false, "data": []any{txn("txn_b", "charge", "b", 500, 30), txn("txn_fee", "fee", "x", -10, 0)}}
		case r.URL.Path == "/v1/balance_transactions":
			// the first page has more; the next request must start after its last id, the refund (which is not a payment)
			body = map[string]any{"has_more": true, "data": []any{txn("txn_a", "charge", "a", 1000, 59), txn("txn_r", "refund", "a", -100, 0)}}
		case r.URL.Path == "/v1/payouts":
			body = map[string]any{"has_more": false, "data": []any{
				map[string]any{"id": "po_1", "amount": 941, "currency": "usd", "created": 1_700_100_000, "arrival_date": 1_700_300_000, "reconciliation_status": "completed"},
				map[string]any{"id": "po_2", "amount": 100, "currency": "usd", "created": 1_700_100_000, "arrival_date": 1_700_300_000, "reconciliation_status": "not_applicable"},
			}}
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: srv.URL}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Settlement reads payments from the balance transactions (by the PaymentIntent of the charge, the reference the ledger holds), marks the ones a completed payout
// carried as settled on its arrival date in its batch, reads the payout itself as a line, and ignores the other reporting categories.
func TestSettlementReadsPaymentsAndPayouts(t *testing.T) {
	var calls []string
	c := settlementFake(t, &calls)
	lines, err := c.Settlement(context.Background(), time.Unix(1_699_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]int{}
	for i, l := range lines {
		by[l.Ref] = i
	}
	a, ok := by["txn_a"]
	if !ok {
		t.Fatalf("lines = %+v, want txn_a", lines)
	}
	la := lines[a]
	if la.Kind != "payment" || la.PaymentRef != "pi_a" || la.Currency != "USD" || la.GrossMinor != 1000 || la.FeeMinor != 59 || la.NetMinor != 941 {
		t.Errorf("payment line = %+v", la)
	}
	if la.AvailableOn == nil || la.SettledAt == nil || la.Batch != "po_1" || la.SettledAt.Unix() != 1_700_300_000 {
		t.Errorf("a payment of a completed payout is settled on its arrival date in its batch: %+v", la)
	}
	if i, ok := by["po_1"]; !ok || lines[i].Kind != "payout" || lines[i].GrossMinor != 941 || lines[i].Batch != "po_1" {
		t.Errorf("payout line = %+v", lines)
	}
	if _, ok := by["txn_r"]; ok {
		t.Error("a refund was read as a payment")
	}
	if _, ok := by["txn_fee"]; ok {
		t.Error("a fee was read as a payment")
	}
	if i, ok := by["txn_b"]; ok && lines[i].SettledAt != nil {
		t.Errorf("a payment no payout carried is not settled: %+v", lines[i])
	}
}

// A balance transaction whose source was not expanded still names the payment by its id; one without a source names nothing.
func TestSettlementSourceReferences(t *testing.T) {
	for in, want := range map[string]string{`"ch_1"`: "ch_1", `{"id":"ch_2","payment_intent":"pi_2"}`: "pi_2", `{"id":"ch_3","payment_intent":null}`: "ch_3", `null`: ""} {
		if got := sourceReference(json.RawMessage(in)); got != want {
			t.Errorf("%s -> %q, want %q", in, got, want)
		}
	}
}
