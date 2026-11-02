package paddle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

type fake struct {
	method, path string
	auth         string
	body         map[string]any
	reply        func() (int, string)
}

func newFake(t *testing.T, tweak func(*Settings)) (*fake, *Client) {
	t.Helper()
	f := &fake{reply: func() (int, string) { return 200, `{"data":{}}` }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.method, f.path, f.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		f.body = nil
		_ = json.Unmarshal(raw, &f.body)
		code, out := f.reply()
		w.WriteHeader(code)
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(srv.Close)
	s := Settings{APIKey: "pdl_key", BaseURL: srv.URL, WebhookSecret: "whsec", UsageProductID: "pro_usage"}
	if tweak != nil {
		tweak(&s)
	}
	c, err := New(s, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestChargeIsPendingAndCarriesTheKey(t *testing.T) {
	f, c := newFake(t, nil)
	_, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "inv:charge:0", InvoiceID: "inv", AmountMinor: 1116, Currency: "USD", PaymentMethod: "sub_01"})
	var p *payments.PendingError
	if !errors.As(err, &p) || p.Ref != "inv:charge:0" {
		t.Fatalf("err = %v; a charge Paddle accepted is pending, not a success", err)
	}
	if f.method != "POST" || f.path != "/subscriptions/sub_01/charge" || f.auth != "Bearer pdl_key" {
		t.Errorf("request %s %s auth %q", f.method, f.path, f.auth)
	}
	if f.body["effective_from"] != "immediately" || f.body["on_payment_failure"] != "prevent_change" {
		t.Errorf("body = %v", f.body)
	}
	item := f.body["items"].([]any)[0].(map[string]any)
	price := item["price"].(map[string]any)
	unit := price["unit_price"].(map[string]any)
	cd := price["custom_data"].(map[string]any)
	if price["product_id"] != "pro_usage" || unit["amount"] != "1116" || unit["currency_code"] != "USD" || price["tax_mode"] != "account_setting" ||
		cd["payrudder_key"] != "inv:charge:0" || item["quantity"].(float64) != 1 {
		t.Errorf("item = %v", item)
	}
}

func TestChargeErrors(t *testing.T) {
	req := payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "i", AmountMinor: 100, Currency: "USD", PaymentMethod: "sub_01"}
	t.Run("a refused request is permanent, not transient and not a decline", func(t *testing.T) {
		f, c := newFake(t, nil)
		f.reply = func() (int, string) {
			return 400, `{"error":{"type":"request_error","code":"subscription_locked_pending_changes","detail":"locked"}}`
		}
		_, err := c.Charge(context.Background(), req)
		var e *Error
		var tr *payments.TransientError
		var d *payments.DeclinedError
		if !errors.As(err, &e) || e.Code != "subscription_locked_pending_changes" || errors.As(err, &tr) || errors.As(err, &d) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("server errors and rate limits are transient", func(t *testing.T) {
		for _, code := range []int{500, 503, 429} {
			f, c := newFake(t, nil)
			f.reply = func() (int, string) { return code, `{}` }
			var tr *payments.TransientError
			if _, err := c.Charge(context.Background(), req); !errors.As(err, &tr) {
				t.Errorf("status %d: %v", code, err)
			}
		}
	})
	t.Run("bad input is refused before any request", func(t *testing.T) {
		f, c := newFake(t, nil)
		for name, mutate := range map[string]func(*payments.ChargeRequest){
			"no key":          func(r *payments.ChargeRequest) { r.IdempotencyKey = "" },
			"not a sub id":    func(r *payments.ChargeRequest) { r.PaymentMethod = "pm_sandbox_success" },
			"nothing to bill": func(r *payments.ChargeRequest) { r.AmountMinor = 0 },
		} {
			r := req
			mutate(&r)
			if _, err := c.Charge(context.Background(), r); err == nil {
				t.Errorf("%s: want an error", name)
			}
		}
		if f.method != "" {
			t.Error("a request was sent")
		}
	})
}

func TestRefundAndCheckoutAndStatus(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func() (int, string) { return 201, `{"data":{"id":"adj_01"}}` }
	res, err := c.Refund(context.Background(), payments.RefundRequest{IdempotencyKey: "r-1", ChargeID: "txn_01", Currency: "USD"})
	if err != nil || res.RefundID != "adj_01" {
		t.Fatalf("Refund = %+v, %v", res, err)
	}
	if f.path != "/adjustments" || f.body["action"] != "refund" || f.body["type"] != "full" || f.body["transaction_id"] != "txn_01" {
		t.Errorf("refund request %s %v", f.path, f.body)
	}
	if _, err := c.Refund(context.Background(), payments.RefundRequest{ChargeID: "ch_1"}); err == nil {
		t.Error("a refund against something that is not a transaction id must fail")
	}

	f.reply = func() (int, string) {
		return 201, `{"data":{"id":"txn_02","checkout":{"url":"https://pay.example/checkout?txn=txn_02"}}}`
	}
	co, err := c.CreateCheckout(context.Background(), payments.CheckoutRequest{PriceRef: "pri_01", Quantity: 2, PayrudderSubscriptionID: "sub-q-1"})
	if err != nil || co.URL != "https://pay.example/checkout?txn=txn_02" || co.Ref != "txn_02" {
		t.Fatalf("CreateCheckout = %+v, %v", co, err)
	}
	item := f.body["items"].([]any)[0].(map[string]any)
	if f.path != "/transactions" || item["price_id"] != "pri_01" || item["quantity"].(float64) != 2 ||
		f.body["custom_data"].(map[string]any)["payrudder_subscription_id"] != "sub-q-1" || f.body["collection_mode"] != "automatic" {
		t.Errorf("checkout request %s %v", f.path, f.body)
	}
	f.reply = func() (int, string) { return 201, `{"data":{"id":"txn_03","checkout":null}}` }
	if _, err := c.CreateCheckout(context.Background(), payments.CheckoutRequest{PriceRef: "pri_01"}); err == nil {
		t.Error("a transaction without a checkout url must fail")
	}
	if _, err := c.CreateCheckout(context.Background(), payments.CheckoutRequest{PriceRef: "nope"}); err == nil {
		t.Error("a price that is not a Paddle price id must fail")
	}

	st, err := c.Status(context.Background(), "k")
	var p *payments.PendingError
	if !errors.As(err, &p) || st.State == payments.StateNotFound {
		t.Errorf("Status = %+v, %v; it must never say not found", st, err)
	}
}

func signed(t *testing.T, c *Client, body string, at time.Time) http.Header {
	t.Helper()
	h := http.Header{}
	h.Set("Paddle-Signature", SignatureHeader(c.s.WebhookSecret, at, []byte(body)))
	return h
}

func TestSignature(t *testing.T) {
	_, c := newFake(t, nil)
	body := `{"event_id":"evt_1","event_type":"transaction.created","data":{}}`
	now := time.Now()
	if _, err := c.ConfirmNotification(context.Background(), []byte(body), signed(t, c, body, now)); err != nil {
		t.Fatalf("a valid signature: %v", err)
	}
	for name, h := range map[string]http.Header{
		"missing":         {},
		"garbage":         {"Paddle-Signature": {"nonsense"}},
		"stale":           signed(t, c, body, now.Add(-time.Hour)),
		"from the future": signed(t, c, body, now.Add(time.Hour)),
		"another body":    signed(t, c, `{"event_id":"evt_1","event_type":"x"}`, now),
		"no h1":           {"Paddle-Signature": {"ts=" + SignatureHeader("x", now, nil)[3:13]}},
	} {
		if _, err := c.ConfirmNotification(context.Background(), []byte(body), h); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: err = %v, want ErrBadSignature", name, err)
		}
	}
	// A wrong secret.
	other := http.Header{}
	other.Set("Paddle-Signature", SignatureHeader("another-secret", now, []byte(body)))
	if _, err := c.ConfirmNotification(context.Background(), []byte(body), other); !errors.Is(err, ErrBadSignature) {
		t.Errorf("wrong secret: %v", err)
	}
	// During secret rotation Paddle sends several h1; one valid is enough.
	good := SignatureHeader(c.s.WebhookSecret, now, []byte(body))
	rot := http.Header{}
	rot.Set("Paddle-Signature", good+";h1=deadbeef")
	if _, err := c.ConfirmNotification(context.Background(), []byte(body), rot); err != nil {
		t.Errorf("rotation: %v", err)
	}
}

func confirm(t *testing.T, c *Client, event string) payments.Notification {
	t.Helper()
	n, err := c.ConfirmNotification(context.Background(), []byte(event), signed(t, c, event, time.Now()))
	if err != nil {
		t.Fatalf("ConfirmNotification: %v", err)
	}
	return n
}

func TestNotifications(t *testing.T) {
	_, c := newFake(t, nil)

	t.Run("a completed transaction is a settled charge, found by the key on its price", func(t *testing.T) {
		n := confirm(t, c, `{"event_id":"evt_1","event_type":"transaction.completed","data":{"id":"txn_01","status":"completed","currency_code":"USD",
			"custom_data":null,"subscription_id":"sub_01","items":[{"price":{"custom_data":{"payrudder_key":"inv:charge:0"}},"quantity":1}],
			"details":{"totals":{"grand_total":"1116","total":"1000"}}}}`)
		if n.Kind != payments.KindCharge || n.State != payments.StateSucceeded || n.ProviderRef != "txn_01" || n.Ref.IdempotencyKey != "inv:charge:0" ||
			n.AmountMinor != 1116 || n.Currency != "USD" || n.Ref.EventID != "evt_1" {
			t.Fatalf("notification = %+v", n)
		}
	})
	t.Run("Paddle's own renewal has no key", func(t *testing.T) {
		n := confirm(t, c, `{"event_id":"evt_2","event_type":"transaction.completed","data":{"id":"txn_02","currency_code":"EUR","items":[{"price":{"custom_data":null}}],
			"details":{"totals":{"grand_total":"900"}}}}`)
		if n.Kind != payments.KindCharge || n.Ref.IdempotencyKey != "" || n.AmountMinor != 900 || n.Currency != "EUR" {
			t.Fatalf("notification = %+v", n)
		}
	})
	t.Run("a failed payment is a decline with Paddle's reason", func(t *testing.T) {
		n := confirm(t, c, `{"event_id":"evt_3","event_type":"transaction.payment_failed","data":{"id":"txn_03","currency_code":"USD",
			"items":[{"price":{"custom_data":{"payrudder_key":"k"}}}],"details":{"totals":{"grand_total":"500"}},
			"payments":[{"status":"error","error_code":"not_enough_balance"},{"status":"error","error_code":"declined"}]}}`)
		// payments[] is sorted newest first (get-transaction, read 2026-11-02): the reason of this failure is the first, not the last.
		if n.State != payments.StateDeclined || n.DeclineCode != "not_enough_balance" || n.Ref.IdempotencyKey != "k" {
			t.Fatalf("notification = %+v", n)
		}
	})
	t.Run("a subscription event carries our subscription id", func(t *testing.T) {
		n := confirm(t, c, `{"event_id":"evt_4","event_type":"subscription.created","data":{"id":"sub_01","status":"active",
			"custom_data":{"quotify_subscription_id":"q-1"},"next_billed_at":"2027-02-01T00:00:00Z"}}`)
		if n.Kind != payments.KindSubscription || n.Subscription == nil || n.Subscription.ProviderRef != "sub_01" || n.Subscription.Status != "active" ||
			n.Subscription.PayrudderSubscriptionID != "q-1" || n.Subscription.NextBilledAt == nil || n.Subscription.NextBilledAt.Year() != 2027 {
			t.Fatalf("notification = %+v", n)
		}
	})
	t.Run("a refund adjustment counts once approved", func(t *testing.T) {
		approved := confirm(t, c, `{"event_id":"evt_5","event_type":"adjustment.updated","data":{"id":"adj_01","action":"refund","status":"approved","transaction_id":"txn_01","currency_code":"USD","totals":{"total":"1116"}}}`)
		if approved.Kind != payments.KindRefund || approved.State != payments.StateSucceeded || approved.ProviderRef != "adj_01" || approved.AmountMinor != 1116 {
			t.Fatalf("approved = %+v", approved)
		}
		rejected := confirm(t, c, `{"event_id":"evt_6","event_type":"adjustment.updated","data":{"id":"adj_02","action":"refund","status":"rejected","currency_code":"USD","totals":{"total":"5"}}}`)
		if rejected.Kind != payments.KindRefund || rejected.State != payments.StateDeclined {
			t.Fatalf("rejected = %+v", rejected)
		}
		for _, status := range []string{"pending_approval", "reversed"} {
			n := confirm(t, c, `{"event_id":"evt_7","event_type":"adjustment.updated","data":{"id":"adj_03","action":"refund","status":"`+status+`","totals":{"total":"5"}}}`)
			if n.Kind != payments.KindIgnored {
				t.Errorf("%s: %+v; a refund that is not final is ignored", status, n)
			}
		}
		credit := confirm(t, c, `{"event_id":"evt_8","event_type":"adjustment.created","data":{"id":"adj_04","action":"credit","status":"approved","totals":{"total":"5"}}}`)
		if credit.Kind != payments.KindIgnored {
			t.Errorf("a credit is not a refund: %+v", credit)
		}
	})
	t.Run("a chargeback and its warning are disputes, and their reversals end them", func(t *testing.T) {
		adj := func(action, status string) payments.Notification {
			return confirm(t, c, `{"event_id":"evt_c","event_type":"adjustment.created","data":{"id":"adj_cb","action":"`+action+`","status":"`+status+`","transaction_id":"txn_01","currency_code":"EUR","totals":{"total":"1250"}}}`)
		}
		for _, tc := range []struct {
			action, status, id string
			outcome            payments.DisputeOutcome
			formal             bool
		}{
			{"chargeback", "approved", "cb_txn_01", payments.DisputeOpen, true}, {"chargeback", "pending_approval", "cb_txn_01", payments.DisputeOpen, true},
			{"chargeback", "reversed", "cb_txn_01", payments.DisputeWon, true}, {"chargeback_reverse", "approved", "cb_txn_01", payments.DisputeWon, true},
			{"chargeback_warning", "approved", "cbw_txn_01", payments.DisputeOpen, false}, {"chargeback_warning_reverse", "approved", "cbw_txn_01", payments.DisputeWon, false},
		} {
			n := adj(tc.action, tc.status)
			if n.Kind != payments.KindDispute || n.Dispute == nil || n.Dispute.ID != tc.id || n.Dispute.Outcome != tc.outcome || n.Dispute.Formal != tc.formal ||
				n.ProviderRef != "txn_01" || n.AmountMinor != 1250 || n.Currency != "EUR" {
				t.Errorf("%s/%s = %+v %+v, want %s %s", tc.action, tc.status, n, n.Dispute, tc.id, tc.outcome)
			}
		}
		for _, tc := range [][2]string{{"chargeback", "rejected"}, {"chargeback_reverse", "pending_approval"}, {"credit_reverse", "approved"}} {
			if n := adj(tc[0], tc[1]); n.Kind != payments.KindIgnored {
				t.Errorf("%s/%s should be ignored: %+v", tc[0], tc[1], n)
			}
		}
	})
	t.Run("a chargeback's fee is the chargeback_fee of the payout totals, in the payout currency, and only on the action that incurs it", func(t *testing.T) {
		fee := func(action, totals string) *payments.DisputeEvent {
			n := confirm(t, c, `{"event_id":"evt_f","event_type":"adjustment.created","data":{"id":"adj_cb","action":"`+action+`","status":"approved","transaction_id":"txn_01","currency_code":"EUR","totals":{"total":"1250"},"payout_totals":`+totals+`}}`)
			if n.Dispute == nil {
				t.Fatalf("%s: %+v", action, n)
			}
			return n.Dispute
		}
		withFee := `{"currency_code":"USD","total":"1400","chargeback_fee":{"amount":"1500","original":{"amount":"1380","currency_code":"EUR"}}}`
		for _, action := range []string{"chargeback", "chargeback_warning"} {
			if d := fee(action, withFee); d.FeeMinor != 1500 || d.FeeCurrency != "USD" {
				t.Errorf("%s: fee %d %s, want 1500 USD (the amount in the payout currency, not the original)", action, d.FeeMinor, d.FeeCurrency)
			}
		}
		if d := fee("chargeback_reverse", withFee); d.FeeMinor != 0 {
			t.Errorf("a reversal carries a fee of %d: the fee is not given back by this event", d.FeeMinor)
		}
		for _, totals := range []string{`null`, `{"currency_code":"USD","total":"1400"}`, `{"currency_code":"USD","chargeback_fee":null}`, `{"chargeback_fee":{"amount":"1500"}}`} {
			if d := fee("chargeback", totals); d.FeeMinor != 0 || d.FeeCurrency != "" {
				t.Errorf("payout_totals %s: fee %d %s, want none (no amount, or no currency to put it in)", totals, d.FeeMinor, d.FeeCurrency)
			}
		}
	})
	t.Run("other events are ignored", func(t *testing.T) {
		if n := confirm(t, c, `{"event_id":"evt_9","event_type":"customer.created","data":{"id":"ctm_1"}}`); n.Kind != payments.KindIgnored {
			t.Errorf("notification = %+v", n)
		}
	})
}

func TestCancelSubscriptionAndMigrationData(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func() (int, string) { return 200, `{"data":{}}` }
	if err := c.CancelSubscription(context.Background(), "sub_01", true); err != nil {
		t.Fatal(err)
	}
	if f.method != "POST" || f.path != "/subscriptions/sub_01/cancel" || f.body["effective_from"] != "next_billing_period" {
		t.Errorf("cancel at period end: %s %s %v", f.method, f.path, f.body)
	}
	if err := c.CancelSubscription(context.Background(), "sub_01", false); err != nil || f.body["effective_from"] != "immediately" {
		t.Errorf("cancel now: %v %v", err, f.body)
	}
	if err := c.CancelSubscription(context.Background(), "txn_1", true); err == nil {
		t.Error("something that is not a subscription id must be refused")
	}
	f.reply = func() (int, string) { return 404, `{"error":{"code":"not_found","detail":"no such subscription"}}` }
	var e *Error
	if err := c.CancelSubscription(context.Background(), "sub_99", true); !errors.As(err, &e) || e.Status != 404 {
		t.Errorf("a refusal is an *Error: %v", err)
	}
	var _ payments.SubscriptionManager = c

	f.reply = func() (int, string) { return 201, `{"data":{"id":"txn_m","checkout":{"url":"https://pay.example/m"}}}` }
	if _, err := c.CreateCheckout(context.Background(), payments.CheckoutRequest{PriceRef: "pri_01", PayrudderMigrationID: "mig-1"}); err != nil {
		t.Fatal(err)
	}
	if cd := f.body["custom_data"].(map[string]any); cd["payrudder_migration_id"] != "mig-1" || cd["quotify_migration_id"] != nil {
		t.Errorf("checkout custom data = %v", f.body["custom_data"])
	}
	n := confirm(t, c, `{"event_id":"evt_m","event_type":"subscription.created","data":{"id":"sub_new","status":"active","custom_data":{"quotify_migration_id":"mig-1"}}}`)
	if n.Subscription == nil || n.Subscription.PayrudderMigrationID != "mig-1" {
		t.Errorf("notification = %+v", n)
	}
}

func TestParseNotificationAndRegistration(t *testing.T) {
	ref, err := ParseNotification([]byte(`{"event_id":"evt_1","event_type":"x","data":{}}`), nil)
	if err != nil || ref.EventID != "evt_1" || ref.IdempotencyKey != "" {
		t.Fatalf("ref = %+v, %v", ref, err)
	}
	for _, bad := range []string{`{`, `{"event_type":"x"}`, `{"event_id":"e"}`} {
		if _, err := ParseNotification([]byte(bad), nil); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	r := payments.NewRegistry()
	Register(r)
	p, err := r.Build(Name, []byte(`{"api_key":"k","webhook_secret":"s","usage_product_id":"pro_1"}`))
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if caps.Owner() != payments.OwnerProvider || !caps.HostedCheckout || !caps.CalculatesTax || caps.IdempotentCharges || !caps.CanCharge("USD") || caps.CanCharge("TRY") {
		t.Errorf("capabilities = %+v", caps)
	}
	var _ payments.Notifier = p.(*Client)
	var _ payments.CheckoutProvider = p.(*Client)
	for name, settings := range map[string]string{
		"no key":       `{"webhook_secret":"s","usage_product_id":"p"}`,
		"no secret":    `{"api_key":"k","usage_product_id":"p"}`,
		"no product":   `{"api_key":"k","webhook_secret":"s"}`,
		"bad tax mode": `{"api_key":"k","webhook_secret":"s","usage_product_id":"p","tax_mode":"whatever"}`,
		"bad base url": `{"api_key":"k","webhook_secret":"s","usage_product_id":"p","base_url":"::"}`,
		"not json":     `{`,
	} {
		if _, err := Factory([]byte(settings)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if !strings.Contains(SignatureHeader("s", time.Unix(1700000000, 0), []byte("b")), "ts=1700000000;h1=") {
		t.Error("signature header format")
	}
}

// A refusal keeps what Paddle says about it: the fields that are wrong and the request id its support asks for (error.errors[], meta.request_id).
func TestRefusalKeepsFieldsAndRequestID(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func() (int, string) {
		return 400, `{"error":{"type":"request_error","code":"invalid_field","detail":"Invalid request","errors":[{"field":"items[0].quantity","message":"must be at least 1"}]},"meta":{"request_id":"req_123"}}`
	}
	var out map[string]any
	err := c.do(context.Background(), http.MethodGet, "/x", nil, &out)
	var pe *Error
	if !errors.As(err, &pe) || pe.RequestID != "req_123" || len(pe.Fields) != 1 || pe.Fields[0] != "items[0].quantity: must be at least 1" {
		t.Fatalf("err = %#v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "items[0].quantity") || !strings.Contains(msg, "req_123") {
		t.Errorf("the message must carry the fields and the request id: %q", msg)
	}
}

func TestRefundAmounts(t *testing.T) {
	txn := func(currency string, items ...string) string {
		li := make([]string, len(items))
		for i, id := range items {
			li[i] = `{"id":"` + id + `"}`
		}
		return `{"data":{"id":"txn_01","currency_code":"` + currency + `","details":{"totals":{"grand_total":"1116"},"line_items":[` + strings.Join(li, ",") + `]}}}`
	}
	refund := func(c *Client, amount int64, currency string) (payments.RefundResult, error) {
		return c.Refund(context.Background(), payments.RefundRequest{IdempotencyKey: "r", ChargeID: "txn_01", AmountMinor: amount, Currency: currency})
	}
	t.Run("the whole total is a full adjustment", func(t *testing.T) {
		rt, c := newRouter(t, map[string]string{"GET /transactions/txn_01": txn("USD", "txnitm_1"), "POST /adjustments": `{"data":{"id":"adj_01"}}`})
		if _, err := refund(c, 1116, "USD"); err != nil {
			t.Fatal(err)
		}
		if b := rt.find("POST", "/adjustments")[0].body; b["type"] != "full" || b["items"] != nil {
			t.Errorf("adjustment = %v", b)
		}
	})
	t.Run("less than the total, one line item: a partial adjustment of that item", func(t *testing.T) {
		rt, c := newRouter(t, map[string]string{"GET /transactions/txn_01": txn("USD", "txnitm_1"), "POST /adjustments": `{"data":{"id":"adj_02"}}`})
		res, err := refund(c, 400, "USD")
		if err != nil || res.RefundID != "adj_02" {
			t.Fatalf("refund = %+v, %v", res, err)
		}
		b := rt.find("POST", "/adjustments")[0].body
		item := b["items"].([]any)[0].(map[string]any)
		if b["action"] != "refund" || b["type"] != "partial" || b["transaction_id"] != "txn_01" || item["item_id"] != "txnitm_1" || item["type"] != "partial" || item["amount"] != "400" {
			t.Errorf("adjustment = %v", b)
		}
	})
	t.Run("refused without asking Paddle to refund anything", func(t *testing.T) {
		for name, tc := range map[string]struct {
			txn      string
			amount   int64
			currency string
		}{
			"several line items: the amount cannot be split": {txn("USD", "txnitm_1", "txnitm_2"), 400, "USD"},
			"more than the transaction":                      {txn("USD", "txnitm_1"), 2000, "USD"},
			"another currency":                               {txn("EUR", "txnitm_1"), 400, "USD"},
		} {
			rt, c := newRouter(t, map[string]string{"GET /transactions/txn_01": tc.txn, "POST /adjustments": `{"data":{"id":"adj_x"}}`})
			if _, err := refund(c, tc.amount, tc.currency); err == nil {
				t.Errorf("%s: refunded", name)
			}
			if len(rt.find("POST", "/adjustments")) != 0 {
				t.Errorf("%s: an adjustment was created", name)
			}
		}
		_, c := newRouter(t, map[string]string{"GET /transactions/txn_01": txn("USD", "txnitm_1", "txnitm_2")})
		if _, err := refund(c, 400, "USD"); !errors.Is(err, payments.ErrUnsupported) {
			t.Errorf("several line items: %v; want ErrUnsupported", err)
		}
	})
}

func TestRateLimitKeepsRetryAfter(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func() (int, string) {
		return 429, `{"error":{"type":"request_error","code":"subscription_immediate_charge_hour_limit_exceeded","detail":"too many"}}`
	}
	var tr *payments.TransientError
	_, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "i", AmountMinor: 100, Currency: "USD", PaymentMethod: "sub_01"})
	if !errors.As(err, &tr) || tr.RetryAfter != 0 || !strings.Contains(err.Error(), "subscription_immediate_charge_hour_limit_exceeded") {
		t.Fatalf("a 429 without Retry-After = %v; want a transient error that names the code", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"type":"api_error","code":"too_many_requests"}}`)
	}))
	defer srv.Close()
	limited, err := New(Settings{APIKey: "pdl_key", BaseURL: srv.URL, WebhookSecret: "w", UsageProductID: "pro_u"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := limited.CheckHealth(context.Background()); !errors.As(err, &tr) || tr.RetryAfter != 42*time.Second {
		t.Fatalf("a 429 with Retry-After: 42 = %v (%+v)", err, tr)
	}
	for _, h := range []string{"", "soon", "-3", "0"} {
		if got := retryAfter(http.Header{"Retry-After": {h}}); got != 0 {
			t.Errorf("Retry-After %q = %s, want not said", h, got)
		}
	}
}

func TestContentTypeOnlyWithABody(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+":"+r.Header.Get("Content-Type"))
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	c, _ := New(Settings{APIKey: "pdl_key", BaseURL: srv.URL, WebhookSecret: "w", UsageProductID: "pro_u"}, 0)
	_ = c.CheckHealth(context.Background())
	_ = c.do(context.Background(), http.MethodPost, "/x", map[string]string{"a": "b"}, nil)
	if len(got) != 2 || got[0] != "GET:" || got[1] != "POST:application/json" {
		t.Errorf("requests = %v", got)
	}
}

func TestSubscriptionNotificationReadsBothCustomDataSpellingsAndAScheduledChange(t *testing.T) {
	_, c := newFake(t, nil)
	now := confirm(t, c, `{"event_id":"e1","event_type":"subscription.updated","data":{"id":"sub_1","status":"active",
		"custom_data":{"payrudder_subscription_id":"new-1","quotify_subscription_id":"old-1","payrudder_migration_id":"m-new"},
		"scheduled_change":{"action":"cancel","effective_at":"2027-03-01T00:00:00Z","resume_at":null}}}`)
	if s := now.Subscription; s == nil || s.PayrudderSubscriptionID != "new-1" || s.PayrudderMigrationID != "m-new" {
		t.Fatalf("the current spelling must win: %+v", now.Subscription)
	}
	sc := now.Subscription.ScheduledChange
	if sc == nil || sc.Action != "cancel" || sc.EffectiveAt.Year() != 2027 || sc.ResumeAt != nil {
		t.Errorf("a cancellation at the end of the period is still status active: scheduled change = %+v", sc)
	}
	old := confirm(t, c, `{"event_id":"e2","event_type":"subscription.created","data":{"id":"sub_2","status":"active","custom_data":{"quotify_subscription_id":"old-2","quotify_migration_id":"m-old"}}}`)
	if s := old.Subscription; s.PayrudderSubscriptionID != "old-2" || s.PayrudderMigrationID != "m-old" || s.ScheduledChange != nil {
		t.Errorf("an object from before the rename is still read: %+v", s)
	}
	pause := confirm(t, c, `{"event_id":"e3","event_type":"subscription.updated","data":{"id":"sub_3","status":"active",
		"scheduled_change":{"action":"pause","effective_at":"2027-03-01T00:00:00Z","resume_at":"2027-04-01T00:00:00Z"}}}`)
	if sc := pause.Subscription.ScheduledChange; sc == nil || sc.Action != "pause" || sc.ResumeAt == nil || sc.ResumeAt.Month() != 4 {
		t.Errorf("pause = %+v", sc)
	}
}
