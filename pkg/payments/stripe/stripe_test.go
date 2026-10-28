package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

const (
	key    = "sk_test_abc123"
	secret = "whsec_test_secret"
)

// fake is a Stripe that records the requests and answers with what the test sets.
type fake struct {
	srv    *httptest.Server
	last   *http.Request
	form   url.Values
	status int
	body   string
}

func newFake(t *testing.T, status int, body string) *fake {
	t.Helper()
	f := &fake{status: status, body: body}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.last = r
		raw, _ := io.ReadAll(r.Body)
		f.form, _ = url.ParseQuery(string(raw))
		if r.Method == http.MethodGet {
			f.form = r.URL.Query()
		}
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) client(t *testing.T) *Client {
	t.Helper()
	c, err := New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: f.srv.URL}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewValidatesTheSettings(t *testing.T) {
	for name, s := range map[string]Settings{
		"no key":      {WebhookSecret: secret},
		"no secret":   {APIKey: key},
		"a pub key":   {APIKey: "pk_test_abc", WebhookSecret: secret},
		"bare prefix": {APIKey: "sk_test_", WebhookSecret: secret},
		"bad url":     {APIKey: key, WebhookSecret: secret, BaseURL: "not a url"},
	} {
		if _, err := New(s, 0); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestModeComesFromTheKeyAndHostsAreStripesOnly(t *testing.T) {
	for k, want := range map[string]bool{"sk_live_x": true, "rk_live_x": true, "sk_test_x": false, "rk_test_x": false} {
		c, err := New(Settings{APIKey: k, WebhookSecret: secret}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if live, known := c.KeyMode(); !known || live != want {
			t.Errorf("%s: live=%v known=%v", k, live, known)
		}
		if live, known := c.Livemode(); !known || live != want {
			t.Errorf("%s on Stripe's host: live=%v known=%v", k, live, known)
		}
	}
	other, _ := New(Settings{APIKey: "sk_live_x", WebhookSecret: secret, BaseURL: "https://evil.example"}, 0)
	if _, known := other.Livemode(); known {
		t.Error("a host that is not Stripe's is known, so a connecting person could send the key there")
	}
	// A live key in a sandbox environment (and a test key in a live one) is refused before it is sent anywhere.
	live, _ := New(Settings{APIKey: "sk_live_x", WebhookSecret: secret}, 0)
	if err := payments.CheckLivemode(live, "sandbox"); !errors.Is(err, payments.ErrLivemodeMismatch) {
		t.Errorf("live key, sandbox environment: %v", err)
	}
	if _, err := payments.Verify(context.Background(), other, "live"); !errors.Is(err, payments.ErrUnknownEndpoint) {
		t.Errorf("verify on a foreign host: %v", err)
	}
}

func TestChargeConfirmsOffSessionWithTheKeyAndIdempotency(t *testing.T) {
	f := newFake(t, 200, `{"id":"pi_1","status":"succeeded","amount":1250,"currency":"eur"}`)
	res, err := f.client(t).Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "inv-1:charge:1", InvoiceID: "inv-1", AmountMinor: 1250, Currency: "EUR", PaymentMethod: "pm_card"})
	if err != nil || res.ChargeID != "pi_1" {
		t.Fatalf("charge: %+v %v", res, err)
	}
	if f.last.URL.Path != "/v1/payment_intents" || f.last.Header.Get("Authorization") != "Bearer "+key || f.last.Header.Get("Idempotency-Key") != "inv-1:charge:1" {
		t.Errorf("request: %s %v", f.last.URL.Path, f.last.Header)
	}
	for k, v := range map[string]string{"amount": "1250", "currency": "eur", "payment_method": "pm_card", "confirm": "true", "off_session": "true", "metadata[payrudder_key]": "inv-1:charge:1", "metadata[payrudder_invoice_id]": "inv-1"} {
		if f.form.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, f.form.Get(k), v)
		}
	}
}

func TestChargeOutcomes(t *testing.T) {
	charge := func(status int, body string) error {
		f := newFake(t, status, body)
		_, err := f.client(t).Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", AmountMinor: 100, Currency: "USD", PaymentMethod: "pm_x"})
		return err
	}
	var declined *payments.DeclinedError
	if err := charge(402, `{"error":{"type":"card_error","code":"card_declined","decline_code":"insufficient_funds"}}`); !errors.As(err, &declined) || declined.Code != "insufficient_funds" {
		t.Errorf("card error: %v", err)
	}
	if err := charge(200, `{"id":"pi_2","status":"requires_action"}`); !errors.As(err, &declined) || declined.Code != "authentication_required" {
		t.Errorf("3-D Secure: %v", err)
	}
	if err := charge(200, `{"id":"pi_3","status":"requires_payment_method","last_payment_error":{"decline_code":"expired_card"}}`); !errors.As(err, &declined) || declined.Code != "expired_card" {
		t.Errorf("requires a payment method: %v", err)
	}
	var pending *payments.PendingError
	if err := charge(200, `{"id":"pi_4","status":"processing"}`); !errors.As(err, &pending) {
		t.Errorf("processing: %v", err)
	}
	var transient *payments.TransientError
	if err := charge(503, `{}`); !errors.As(err, &transient) {
		t.Errorf("503: %v", err)
	}
	var refused *Error
	if err := charge(400, `{"error":{"type":"invalid_request_error","code":"parameter_invalid_integer","message":"bad"}}`); !errors.As(err, &refused) || refused.Code != "parameter_invalid_integer" {
		t.Errorf("a refused request: %v", err)
	}
}

func TestChargeRefusesWhatStripeWouldRound(t *testing.T) {
	c, _ := New(Settings{APIKey: key, WebhookSecret: secret}, 0)
	r := payments.ChargeRequest{IdempotencyKey: "k", PaymentMethod: "pm_x"}
	for name, mod := range map[string]func(*payments.ChargeRequest){
		"a KWD amount not ending in 0": func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor = "KWD", 1234 },
		"a zero amount":                func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor = "USD", 0 },
		"no payment method id":         func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor, r.PaymentMethod = "USD", 100, "card_x" },
		"no idempotency key":           func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor, r.IdempotencyKey = "USD", 100, "" },
	} {
		x := r
		mod(&x)
		if _, err := c.Charge(context.Background(), x); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestStatusFindsTheIntentByTheKey(t *testing.T) {
	f := newFake(t, 200, `{"data":[{"id":"pi_9","status":"succeeded"}]}`)
	st, err := f.client(t).Status(context.Background(), `inv'1:charge:1`)
	if err != nil || st.State != payments.StateSucceeded || st.ChargeID != "pi_9" {
		t.Fatalf("status: %+v %v", st, err)
	}
	if got := f.form.Get("query"); got != `metadata['payrudder_key']:'inv\'1:charge:1'` {
		t.Errorf("query = %q", got)
	}
	f.body = `{"data":[]}`
	if st, err := f.client(t).Status(context.Background(), "k"); err != nil || st.State != payments.StateNotFound {
		t.Errorf("nothing found: %+v %v", st, err)
	}
	f.body = `{"data":[{"id":"pi_8","status":"requires_payment_method","last_payment_error":{"code":"card_declined"}}]}`
	if st, _ := f.client(t).Status(context.Background(), "k"); st.State != payments.StateDeclined || st.DeclineCode != "card_declined" {
		t.Errorf("declined: %+v", st)
	}
	f.body = `{"data":[{"id":"pi_7","status":"processing"}]}`
	var pending *payments.PendingError
	if _, err := f.client(t).Status(context.Background(), "k"); !errors.As(err, &pending) {
		t.Errorf("processing: %v", err)
	}
}

func TestRefund(t *testing.T) {
	f := newFake(t, 200, `{"id":"re_1","status":"succeeded"}`)
	res, err := f.client(t).Refund(context.Background(), payments.RefundRequest{IdempotencyKey: "inv-1:refund:1", InvoiceID: "inv-1", ChargeID: "pi_1", AmountMinor: 500, Currency: "EUR"})
	if err != nil || res.RefundID != "re_1" {
		t.Fatalf("refund: %+v %v", res, err)
	}
	if f.last.URL.Path != "/v1/refunds" || f.form.Get("payment_intent") != "pi_1" || f.form.Get("amount") != "500" || f.last.Header.Get("Idempotency-Key") != "inv-1:refund:1" {
		t.Errorf("request: %s %v %v", f.last.URL.Path, f.form, f.last.Header)
	}
	if _, err := f.client(t).Refund(context.Background(), payments.RefundRequest{ChargeID: "ch_1"}); err == nil {
		t.Error("a charge id that is not a PaymentIntent was accepted")
	}
	if _, err := f.client(t).Refund(context.Background(), payments.RefundRequest{IdempotencyKey: "k", ChargeID: "pi_1"}); err != nil || f.form.Has("amount") {
		t.Errorf("a whole refund sends no amount: %v %v", err, f.form)
	}
}

func TestInvoiceCheckout(t *testing.T) {
	f := newFake(t, 200, `{"id":"cs_1","url":"https://checkout.stripe.com/c/pay/cs_1"}`)
	co, err := f.client(t).CreateInvoiceCheckout(context.Background(), payments.CheckoutInvoiceRequest{IdempotencyKey: "link:inv-1:1", InvoiceID: "inv-1", Number: "INV-7", AmountMinor: 2000, Currency: "USD",
		CustomerEmail: "a@b.c", ReturnURL: "https://app.example/c/x"})
	if err != nil || co.URL == "" || co.Ref != "cs_1" {
		t.Fatalf("checkout: %+v %v", co, err)
	}
	for k, v := range map[string]string{"mode": "payment", "success_url": "https://app.example/c/x", "line_items[0][price_data][unit_amount]": "2000", "line_items[0][price_data][currency]": "usd",
		"line_items[0][price_data][product_data][name]": "Invoice INV-7", "metadata[payrudder_key]": "link:inv-1:1", "payment_intent_data[metadata][payrudder_key]": "link:inv-1:1", "customer_email": "a@b.c"} {
		if f.form.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, f.form.Get(k), v)
		}
	}
}

func TestHealth(t *testing.T) {
	ok := newFake(t, 200, `{"object":"balance"}`)
	if err := ok.client(t).CheckHealth(context.Background()); err != nil || ok.last.URL.Path != "/v1/balance" {
		t.Errorf("good key: %v", err)
	}
	bad := newFake(t, 401, `{"error":{"type":"invalid_request_error","message":"Invalid API Key"}}`)
	if _, err := payments.Check(context.Background(), bad.client(t)); !errors.Is(err, payments.ErrBadCredentials) {
		t.Errorf("bad key: %v", err)
	}
}

func signed(c *Client, body string, at time.Time) http.Header {
	h := http.Header{}
	h.Set("Stripe-Signature", SignatureHeader(secret, at, []byte(body)))
	return h
}

func TestNotificationsAreVerifiedAndMapped(t *testing.T) {
	c, _ := New(Settings{APIKey: key, WebhookSecret: secret}, 0)
	now := time.Now()
	c.now = func() time.Time { return now }
	confirm := func(body string) (payments.Notification, error) {
		return c.ConfirmNotification(context.Background(), []byte(body), signed(c, body, now))
	}

	n, err := confirm(`{"id":"evt_1","type":"payment_intent.succeeded","created":1790000000,"data":{"object":{"id":"pi_1","amount":1250,"amount_received":1250,"currency":"eur","metadata":{"payrudder_key":"inv-1:charge:1"}}}}`)
	if err != nil || n.Kind != payments.KindCharge || n.State != payments.StateSucceeded || n.Ref.IdempotencyKey != "inv-1:charge:1" || n.Ref.EventID != "evt_1" || n.ProviderRef != "pi_1" || n.AmountMinor != 1250 || n.Currency != "EUR" {
		t.Errorf("succeeded: %+v %v", n, err)
	}
	n, _ = confirm(`{"id":"evt_2","type":"payment_intent.payment_failed","data":{"object":{"id":"pi_2","amount":900,"currency":"usd","metadata":{"payrudder_key":"k2"},"last_payment_error":{"decline_code":"expired_card"}}}}`)
	if n.State != payments.StateDeclined || n.DeclineCode != "expired_card" || n.Ref.IdempotencyKey != "k2" {
		t.Errorf("failed: %+v", n)
	}
	n, _ = confirm(`{"id":"evt_3","type":"checkout.session.completed","data":{"object":{"payment_intent":"pi_3","payment_status":"paid","amount_total":2000,"currency":"usd","metadata":{"payrudder_key":"link:1"}}}}`)
	if n.State != payments.StateSucceeded || n.ProviderRef != "pi_3" || n.Ref.IdempotencyKey != "link:1" || n.AmountMinor != 2000 {
		t.Errorf("checkout paid: %+v", n)
	}
	n, _ = confirm(`{"id":"evt_4","type":"checkout.session.completed","data":{"object":{"payment_intent":"pi_4","payment_status":"unpaid","metadata":{"payrudder_key":"link:2"}}}}`)
	if n.Kind != payments.KindIgnored {
		t.Errorf("a delayed method that has not settled is not an outcome: %+v", n)
	}
	n, _ = confirm(`{"id":"evt_5","type":"refund.updated","data":{"object":{"id":"re_5","status":"succeeded","amount":500,"currency":"eur","metadata":{"payrudder_key":"r5"}}}}`)
	if n.Kind != payments.KindRefund || n.State != payments.StateSucceeded || n.ProviderRef != "re_5" || n.AmountMinor != 500 {
		t.Errorf("refund: %+v", n)
	}
	n, _ = confirm(`{"id":"evt_6","type":"refund.updated","data":{"object":{"id":"re_6","status":"pending"}}}`)
	if n.Kind != payments.KindIgnored {
		t.Errorf("a pending refund is not final: %+v", n)
	}
	n, _ = confirm(`{"id":"evt_7","type":"customer.created","data":{"object":{}}}`)
	if n.Kind != payments.KindIgnored {
		t.Errorf("an event of no use: %+v", n)
	}
}

func TestNotificationsThatDoNotVerifyAreRefused(t *testing.T) {
	c, _ := New(Settings{APIKey: key, WebhookSecret: secret}, 0)
	now := time.Now()
	c.now = func() time.Time { return now }
	body := `{"id":"evt_1","type":"payment_intent.succeeded","data":{"object":{}}}`
	other := http.Header{}
	other.Set("Stripe-Signature", strings.Replace(SignatureHeader("whsec_other", now, []byte(body)), "t=", "t=", 1))
	stale := http.Header{}
	stale.Set("Stripe-Signature", SignatureHeader(secret, now.Add(-time.Hour), []byte(body)))
	for name, h := range map[string]http.Header{"no header": {}, "another secret": other, "a stale timestamp": stale, "garbage": {"Stripe-Signature": {"nonsense"}}} {
		if _, err := c.ConfirmNotification(context.Background(), []byte(body), h); !errors.Is(err, payments.ErrUnverified) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A body changed after it was signed does not verify either.
	if _, err := c.ConfirmNotification(context.Background(), []byte(strings.Replace(body, "succeeded", "failed", 1)), signed(c, body, now)); !errors.Is(err, payments.ErrUnverified) {
		t.Errorf("a changed body: %v", err)
	}
	// Stripe may send several v1 signatures while a secret is rolled: any one that matches is enough.
	h := http.Header{}
	h.Set("Stripe-Signature", "t="+SignatureHeader(secret, now, []byte(body))[2:strings.Index(SignatureHeader(secret, now, []byte(body)), ",")]+",v1=deadbeef,"+SignatureHeader(secret, now, []byte(body))[strings.Index(SignatureHeader(secret, now, []byte(body)), ",")+1:])
	if _, err := c.ConfirmNotification(context.Background(), []byte(body), h); err != nil {
		t.Errorf("a rolled secret: %v", err)
	}
}

func TestParseNotificationNamesTheEventOnly(t *testing.T) {
	ref, err := ParseNotification([]byte(`{"id":"evt_1","type":"payment_intent.succeeded","data":{"object":{"metadata":{"payrudder_key":"forged"}}}}`), nil)
	if err != nil || ref.EventID != "evt_1" || ref.IdempotencyKey != "" {
		t.Errorf("%+v %v", ref, err)
	}
	if _, err := ParseNotification([]byte(`{}`), nil); err == nil {
		t.Error("an event without an id was accepted")
	}
	var _ = json.Marshal
}

func TestRegisteredLikeTheOthers(t *testing.T) {
	r := payments.NewRegistry()
	Register(r)
	p, err := r.Build("stripe~eu", []byte(`{"api_key":"sk_test_x","webhook_secret":"whsec_x"}`))
	if err != nil || p.Name() != "stripe" {
		t.Fatalf("build: %v", err)
	}
	if _, ok := r.Parser("stripe~eu"); !ok {
		t.Error("no webhook parser for a further account")
	}
	if c := p.Capabilities(); !c.HostedCheckout || !c.Refunds || !c.IdempotentCharges || c.OwnsRenewal || c.OwnsDunning || !c.CanCharge("USD") {
		t.Errorf("capabilities: %+v", c)
	}
}
