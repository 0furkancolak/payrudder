package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
	var action *payments.ActionRequiredError
	if err := charge(200, `{"id":"pi_2","status":"requires_action"}`); !errors.As(err, &action) || action.Ref != "pi_2" || errors.As(err, &declined) {
		t.Errorf("3-D Secure: %v, want the customer to be asked to act, not a decline", err)
	}
	// Off session Stripe answers 402 with the PaymentIntent in the error (docs.stripe.com/api/errors): a decline keeps its reference, authentication is no decline.
	if err := charge(402, `{"error":{"type":"card_error","code":"authentication_required","payment_intent":{"id":"pi_5","status":"requires_payment_method"}}}`); !errors.As(err, &action) || action.Ref != "pi_5" {
		t.Errorf("authentication_required: %v", err)
	}
	if err := charge(402, `{"error":{"type":"card_error","code":"card_declined","decline_code":"insufficient_funds","payment_intent":{"id":"pi_6"}}}`); !errors.As(err, &declined) || declined.Ref != "pi_6" {
		t.Errorf("a decline keeps the PaymentIntent: %v", err)
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
		"a KWD amount not ending in 0":           func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor = "KWD", 1234 },
		"a zero amount":                          func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor = "USD", 0 },
		"ISK, which Stripe counts in hundredths": func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor = "ISK", 500 },
		"UGX, likewise":                          func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor = "UGX", 500 },
		"no payment method id":                   func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor, r.PaymentMethod = "USD", 100, "card_x" },
		"no idempotency key":                     func(r *payments.ChargeRequest) { r.Currency, r.AmountMinor, r.IdempotencyKey = "USD", 100, "" },
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
	// A payment that waits for the customer is waiting, not declined: the saga must not start dunning for it.
	for _, body := range []string{`{"data":[{"id":"pi_6","status":"requires_action"}]}`, `{"data":[{"id":"pi_6","status":"requires_payment_method","last_payment_error":{"code":"authentication_required"}}]}`} {
		f.body = body
		if st, err := f.client(t).Status(context.Background(), "k"); err != nil || st.State != payments.StateActionRequired || st.ChargeID != "pi_6" {
			t.Errorf("%s: %+v %v", body, st, err)
		}
	}
}

// With the reference of the first response the PaymentIntent is fetched by its id, with no search; a reference that is not the charge's is refused.
func TestStatusByRefRetrievesTheIntent(t *testing.T) {
	f := newFake(t, 200, `{"id":"pi_9","status":"succeeded","metadata":{"payrudder_key":"inv-1:charge:0"}}`)
	st, err := f.client(t).StatusByRef(context.Background(), "pi_9", "inv-1:charge:0")
	if err != nil || st.State != payments.StateSucceeded || st.ChargeID != "pi_9" {
		t.Fatalf("status: %+v %v", st, err)
	}
	if f.last.Method != http.MethodGet || f.last.URL.Path != "/v1/payment_intents/pi_9" || f.last.URL.RawQuery != "" {
		t.Errorf("request: %s %s?%s, want a GET of the intent and no search", f.last.Method, f.last.URL.Path, f.last.URL.RawQuery)
	}
	if _, err := f.client(t).StatusByRef(context.Background(), "pi_9", "inv-2:charge:0"); err == nil {
		t.Error("an intent of another charge was taken for this one")
	}
	if _, err := f.client(t).StatusByRef(context.Background(), "ch_9", "inv-1:charge:0"); err == nil {
		t.Error("a reference that is not a PaymentIntent was accepted")
	}
	f.body = `{"id":"pi_9","status":"requires_action","metadata":{"payrudder_key":"inv-1:charge:0"}}`
	if st, err := f.client(t).StatusByRef(context.Background(), "pi_9", "inv-1:charge:0"); err != nil || st.State != payments.StateActionRequired {
		t.Errorf("requires_action: %+v %v", st, err)
	}
	f.body = `{"id":"pi_9","status":"requires_payment_method","last_payment_error":{"decline_code":"expired_card","advice_code":"do_not_try_again"},"metadata":{"payrudder_key":"inv-1:charge:0"}}`
	if st, err := f.client(t).StatusByRef(context.Background(), "pi_9", "inv-1:charge:0"); err != nil || st.State != payments.StateDeclined || st.DeclineCode != "expired_card" || !st.Final {
		t.Errorf("declined: %+v %v", st, err)
	}
	f.body = `{"id":"pi_9","status":"processing","metadata":{"payrudder_key":"inv-1:charge:0"}}`
	var pending *payments.PendingError
	if _, err := f.client(t).StatusByRef(context.Background(), "pi_9", "inv-1:charge:0"); !errors.As(err, &pending) {
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
	if c := p.Capabilities(); !c.HostedCheckout || !c.Refunds || !c.IdempotentCharges || !c.IdempotentRefunds || c.OwnsRenewal || c.OwnsDunning || !c.CanCharge("USD") {
		t.Errorf("capabilities: %+v", c)
	}
}

// A refund Stripe says failed or canceled moved no money: it is not booked as done. A pending one (it settles later) is accepted.
func TestRefundStatus(t *testing.T) {
	for status, wantErr := range map[string]bool{"succeeded": false, "pending": false, "requires_action": false, "failed": true, "canceled": true} {
		f := newFake(t, 200, `{"id":"re_1","status":"`+status+`"}`)
		_, err := f.client(t).Refund(context.Background(), payments.RefundRequest{IdempotencyKey: "k", ChargeID: "pi_1", AmountMinor: 500, Currency: "EUR"})
		if (err != nil) != wantErr {
			t.Errorf("status %q: err = %v, want error %v", status, err, wantErr)
		}
	}
}

// Every request names the API version, so the account's default moving does not change what the adapter reads.
func TestEveryRequestPinsTheAPIVersion(t *testing.T) {
	f := newFake(t, 200, `{"id":"pi_1","status":"succeeded"}`)
	c := f.client(t)
	_, _ = c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", AmountMinor: 100, Currency: "USD", PaymentMethod: "pm_x"})
	if got := f.last.Header.Get("Stripe-Version"); got != APIVersion || APIVersion != "2026-09-30.endive" {
		t.Errorf("Stripe-Version = %q, want %q", got, APIVersion)
	}
	_ = c.CheckHealth(context.Background())
	if got := f.last.Header.Get("Stripe-Version"); got != APIVersion {
		t.Errorf("on a GET: Stripe-Version = %q", got)
	}
	pinned, err := New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: f.srv.URL, APIVersion: "2026-10-28.endive"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = pinned.CheckHealth(context.Background())
	if got := f.last.Header.Get("Stripe-Version"); got != "2026-10-28.endive" {
		t.Errorf("an override: Stripe-Version = %q", got)
	}
}

// A payment method attached to a Customer is charged with that Customer.
func TestChargeWithTheCustomerThePaymentMethodIsAttachedTo(t *testing.T) {
	f := newFake(t, 200, `{"id":"pi_1","status":"succeeded"}`)
	req := payments.ChargeRequest{IdempotencyKey: "k", AmountMinor: 100, Currency: "USD"}
	req.PaymentMethod = "cus_A:pm_B"
	if _, err := f.client(t).Charge(context.Background(), req); err != nil || f.form.Get("customer") != "cus_A" || f.form.Get("payment_method") != "pm_B" {
		t.Fatalf("charge with a customer: %v %v", err, f.form)
	}
	req.PaymentMethod = "pm_B"
	if _, err := f.client(t).Charge(context.Background(), req); err != nil || f.form.Has("customer") || f.form.Get("payment_method") != "pm_B" {
		t.Fatalf("charge without one: %v %v", err, f.form)
	}
	for _, bad := range []string{"cus_A:card_B", "acct_A:pm_B", ":pm_B", "cus_A:"} {
		req.PaymentMethod = bad
		if _, err := f.client(t).Charge(context.Background(), req); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRefusalsAreRetriedAsStripeSays(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		header map[string]string
		body   string
		want   string
	}{
		"500":                                  {500, nil, `{}`, "transient"},
		"a rate limit":                         {429, map[string]string{"Stripe-Rate-Limited-Reason": "endpoint-rate"}, `{"error":{"type":"invalid_request_error","code":"rate_limit"}}`, "transient"},
		"another request with the key is busy": {409, nil, `{"error":{"type":"invalid_request_error","code":"idempotency_key_in_use"}}`, "transient"},
		"a dependency outside Stripe failed":   {424, nil, `{"error":{"type":"api_error"}}`, "transient"},
		"Stripe says retry":                    {400, map[string]string{"Stripe-Should-Retry": "true"}, `{}`, "transient"},
		"Stripe says do not retry a 500":       {500, map[string]string{"Stripe-Should-Retry": "false"}, `{}`, "refused"},
		"Stripe says do not retry a 409":       {409, map[string]string{"Stripe-Should-Retry": "false"}, `{}`, "refused"},
		"a request Stripe refused":             {400, nil, `{"error":{"type":"invalid_request_error","code":"parameter_invalid_integer"}}`, "refused"},
		"a key used with other parameters":     {400, nil, `{"error":{"type":"idempotency_error","code":"idempotency_key_in_use"}}`, "refused"},
		"a wrong key":                          {401, nil, `{"error":{"type":"invalid_request_error"}}`, "refused"},
		"a card error":                         {402, nil, `{"error":{"type":"card_error","code":"card_declined","decline_code":"do_not_honor"}}`, "decline do_not_honor"},
		"authentication that was not handled":  {402, nil, `{"error":{"type":"card_error","code":"card_declined","decline_code":"authentication_not_handled"}}`, "action required"},
		"authentication required":              {402, nil, `{"error":{"type":"card_error","code":"authentication_required"}}`, "action required"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for k, v := range tc.header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		c, _ := New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: srv.URL}, 0)
		_, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", AmountMinor: 100, Currency: "USD", PaymentMethod: "pm_x"})
		srv.Close()
		var tr *payments.TransientError
		var d *payments.DeclinedError
		var a *payments.ActionRequiredError
		var e *Error
		got := "other"
		switch {
		case errors.As(err, &tr):
			got = "transient"
		case errors.As(err, &a):
			got = "action required"
		case errors.As(err, &d):
			got = "decline " + d.Code
		case errors.As(err, &e):
			got = "refused"
		}
		if got != tc.want {
			t.Errorf("%s: %v = %s, want %s", name, err, got, tc.want)
		}
		if tc.header["Stripe-Rate-Limited-Reason"] != "" && !strings.Contains(err.Error(), "endpoint-rate") {
			t.Errorf("%s: the rate limit reason is lost: %v", name, err)
		}
	}
}

// The balance says which mode the account is in; a key whose prefix says otherwise is refused.
func TestHealthComparesTheKeyModeWithTheAccountMode(t *testing.T) {
	for body, wantErr := range map[string]bool{`{"object":"balance","livemode":false}`: false, `{"object":"balance","livemode":true}`: true, `{"object":"balance"}`: false} {
		f := newFake(t, 200, body)
		err := f.client(t).CheckHealth(context.Background()) // an sk_test_ key
		if (err != nil) != wantErr || wantErr && !errors.Is(err, payments.ErrLivemodeMismatch) {
			t.Errorf("%s: %v, want error %v", body, err, wantErr)
		}
	}
}

// Saving the customer is off unless the account asks, and then it is the documented pair: a Customer is created and the method is kept for off-session use.
func TestInvoiceCheckoutSavesTheCustomerOnlyWhenAsked(t *testing.T) {
	req := payments.CheckoutInvoiceRequest{IdempotencyKey: "link:inv-1:1", InvoiceID: "inv-1", AmountMinor: 2000, Currency: "USD", ReturnURL: "https://app.example/c/x"}
	f := newFake(t, 200, `{"id":"cs_1","url":"https://checkout.stripe.com/c/pay/cs_1"}`)
	if _, err := f.client(t).CreateInvoiceCheckout(context.Background(), req); err != nil || f.form.Has("customer_creation") || f.form.Has("payment_intent_data[setup_future_usage]") {
		t.Fatalf("by default: %v %v", err, f.form)
	}
	saving, _ := New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: f.srv.URL, SaveCustomer: true}, 0)
	if _, err := saving.CreateInvoiceCheckout(context.Background(), req); err != nil || f.form.Get("customer_creation") != "always" || f.form.Get("payment_intent_data[setup_future_usage]") != "off_session" {
		t.Fatalf("with save_customer: %v %v", err, f.form)
	}
}

// These events exist at Stripe and are deliberately not acted on (docs/stripe-compatibility-matrix.md, finding 8).
func TestEventsThatAreDeliberatelyIgnored(t *testing.T) {
	c, _ := New(Settings{APIKey: key, WebhookSecret: secret}, 0)
	now := time.Now()
	c.now = func() time.Time { return now }
	for _, typ := range []string{"payment_intent.requires_action", "payment_intent.processing", "setup_intent.succeeded", "checkout.session.expired", "payment_method.attached", "charge.refund.updated"} {
		body := `{"id":"evt_x","type":"` + typ + `","data":{"object":{"id":"x","status":"succeeded","metadata":{"payrudder_key":"k"}}}}`
		n, err := c.ConfirmNotification(context.Background(), []byte(body), signed(c, body, now))
		if err != nil || n.Kind != payments.KindIgnored {
			t.Errorf("%s: %+v %v", typ, n, err)
		}
	}
	// A completed session with nothing to pay (a zero total) settles nothing. A setup is another matter: see TestASavedCardIsLearnedFromTheSetupSession.
	body := `{"id":"evt_y","type":"checkout.session.completed","data":{"object":{"payment_status":"no_payment_required","metadata":{"payrudder_key":"k"}}}}`
	if n, err := c.ConfirmNotification(context.Background(), []byte(body), signed(c, body, now)); err != nil || n.Kind != payments.KindIgnored {
		t.Errorf("no_payment_required: %+v %v", n, err)
	}
}

// Stripe's advice_code do_not_try_again is the only thing its documentation marks as "do not retry" (docs.stripe.com/declines/card.md): it makes a decline final, on a
// refused charge, on a PaymentIntent a status query finds. Every other advice, and every decline code (stolen_card, fraudulent...), leaves it retryable.
func TestDoNotTryAgainMakesADeclineFinal(t *testing.T) {
	ctx := context.Background()
	req := payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "inv-1", AmountMinor: 1000, Currency: "EUR", PaymentMethod: "pm_card"}
	for _, c := range []struct {
		name, body string
		final      bool
	}{
		{"do not try again", `{"error":{"type":"card_error","code":"card_declined","decline_code":"generic_decline","advice_code":"do_not_try_again"}}`, true},
		{"try again later", `{"error":{"type":"card_error","code":"card_declined","decline_code":"generic_decline","advice_code":"try_again_later"}}`, false},
		{"confirm card data", `{"error":{"type":"card_error","code":"card_declined","decline_code":"incorrect_cvc","advice_code":"confirm_card_data"}}`, false},
		{"no advice, a stolen card", `{"error":{"type":"card_error","code":"card_declined","decline_code":"stolen_card"}}`, false},
	} {
		_, err := newFake(t, 402, c.body).client(t).Charge(ctx, req)
		var d *payments.DeclinedError
		if !errors.As(err, &d) || d.Final != c.final {
			t.Errorf("%s: %v, want a decline with Final=%v", c.name, err, c.final)
		}
	}

	// A PaymentIntent that comes back declined from a confirm (200 with requires_payment_method) and from a status query carries it in last_payment_error.
	pi := `{"id":"pi_9","status":"requires_payment_method","last_payment_error":{"code":"card_declined","decline_code":"generic_decline","advice_code":"do_not_try_again"}}`
	_, err := newFake(t, 200, pi).client(t).Charge(ctx, req)
	var d *payments.DeclinedError
	if !errors.As(err, &d) || !d.Final || d.Code != "generic_decline" {
		t.Errorf("charge answered with a declined intent: %v", err)
	}
	st, err := newFake(t, 200, `{"data":[`+pi+`]}`).client(t).Status(ctx, "k")
	if err != nil || st.State != payments.StateDeclined || !st.Final {
		t.Errorf("status: %+v %v", st, err)
	}
	st, err = newFake(t, 200, `{"data":[{"id":"pi_9","status":"requires_payment_method","last_payment_error":{"decline_code":"generic_decline","advice_code":"try_again_later"}}]}`).client(t).Status(ctx, "k")
	if err != nil || st.State != payments.StateDeclined || st.Final {
		t.Errorf("status with another advice: %+v %v", st, err)
	}
}

// The webhook endpoint is created at Stripe: its address, exactly the events the adapter handles, the API version the adapter reads, and a key that repeats for the same
// request; the signing secret Stripe shows only then is handed back.
func TestRegisterWebhookCreatesTheEndpointAndReturnsTheSecret(t *testing.T) {
	f := newFake(t, 200, `{"id":"we_1","object":"webhook_endpoint","secret":"whsec_new","enabled_events":["payment_intent.succeeded"],"status":"enabled"}`)
	c, err := New(Settings{APIKey: key, BaseURL: f.srv.URL}, 0) // connected before any endpoint exists: no signing secret yet
	if err != nil {
		t.Fatal(err)
	}
	ep, err := c.RegisterWebhook(context.Background(), payments.WebhookRegistration{URL: "https://pay.example.com/webhooks/stripe/t/live"})
	if err != nil || ep.ID != "we_1" || ep.Secret != "whsec_new" {
		t.Fatalf("register: %+v %v", ep, err)
	}
	if f.last.Method != http.MethodPost || f.last.URL.Path != "/v1/webhook_endpoints" || f.last.Header.Get("Stripe-Version") != APIVersion {
		t.Errorf("request: %s %s %v", f.last.Method, f.last.URL.Path, f.last.Header)
	}
	if f.form.Get("url") != "https://pay.example.com/webhooks/stripe/t/live" || f.form.Get("api_version") != APIVersion {
		t.Errorf("form: %v", f.form)
	}
	var sent []string
	for i := range WebhookEvents {
		sent = append(sent, f.form.Get("enabled_events["+strconv.Itoa(i)+"]"))
	}
	if strings.Join(sent, ",") != strings.Join(WebhookEvents, ",") || f.form.Get("connect") != "" {
		t.Errorf("events sent %v, want exactly %v and no connect endpoint", sent, WebhookEvents)
	}
	first := f.last.Header.Get("Idempotency-Key")
	if _, err := c.RegisterWebhook(context.Background(), payments.WebhookRegistration{URL: "https://pay.example.com/webhooks/stripe/t/live"}); err != nil || f.last.Header.Get("Idempotency-Key") != first || first == "" {
		t.Errorf("the same request must repeat its key: %q then %q (%v)", first, f.last.Header.Get("Idempotency-Key"), err)
	}
	if _, err := c.RegisterWebhook(context.Background(), payments.WebhookRegistration{URL: "https://pay.example.com/webhooks/stripe/t/sandbox"}); err != nil || f.last.Header.Get("Idempotency-Key") == first {
		t.Errorf("another address must not reuse the key")
	}
	for _, bad := range []string{"", "http://pay.example.com/hook", "not a url", "https://"} {
		if _, err := c.RegisterWebhook(context.Background(), payments.WebhookRegistration{URL: bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// An answer without the secret is no registration (it could never be verified).
	if _, err := newFake(t, 200, `{"id":"we_2"}`).client(t).RegisterWebhook(context.Background(), payments.WebhookRegistration{URL: "https://pay.example.com/h"}); err == nil {
		t.Error("an endpoint without a secret was accepted")
	}
}

// An account connected before its webhook endpoint exists believes no notification: an empty secret must never verify anything (an HMAC with an empty key is forgeable).
func TestWithoutASigningSecretNothingIsBelieved(t *testing.T) {
	c, err := New(Settings{APIKey: key}, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"evt_1","type":"payment_intent.succeeded","created":1,"data":{"object":{"id":"pi_1"}}}`)
	h := http.Header{}
	h.Set("Stripe-Signature", SignatureHeader("", time.Now(), body)) // signed with the empty key
	if _, err := c.ConfirmNotification(context.Background(), body, h); !errors.Is(err, payments.ErrUnverified) {
		t.Fatalf("a body signed with an empty key: %v, want ErrUnverified", err)
	}
}

// stripeAPI answers each path with what the test sets and records the requests: the setup flow talks to three endpoints.
type stripeAPI struct {
	srv   *httptest.Server
	reqs  []string
	forms []url.Values
	keys  []string
	bodys map[string]string
}

func newStripeAPI(t *testing.T, bodys map[string]string) *stripeAPI {
	t.Helper()
	f := &stripeAPI{bodys: bodys}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		f.reqs, f.forms, f.keys = append(f.reqs, r.Method+" "+r.URL.Path), append(f.forms, form), append(f.keys, r.Header.Get("Idempotency-Key"))
		body, ok := f.bodys[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stripeAPI) client(t *testing.T) *Client {
	t.Helper()
	c, err := New(Settings{APIKey: key, WebhookSecret: secret, BaseURL: f.srv.URL}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A customer who has no Stripe Customer yet gets one (once: its key is the Payrudder customer's), and the page is a Checkout Session in setup mode on that Customer, in
// the tenant's currency, tagged with who it is for. A customer Payrudder already knows at Stripe makes no second one.
func TestSetupCheckoutMakesTheCustomerOnceAndASessionInSetupMode(t *testing.T) {
	api := newStripeAPI(t, map[string]string{"POST /v1/customers": `{"id":"cus_1"}`, "POST /v1/checkout/sessions": `{"id":"cs_1","url":"https://checkout.stripe.com/c/pay/cs_1"}`})
	c := api.client(t)
	req := payments.SetupRequest{PayrudderCustomerID: "0192-cust", ExternalID: "user-42", Email: "ada@example.com", Name: "Ada", Currency: "EUR", ReturnURL: "https://pay.example.com/back", IdempotencyKey: "setup-1"}
	co, err := c.CreateSetupCheckout(context.Background(), req)
	if err != nil || co.URL != "https://checkout.stripe.com/c/pay/cs_1" || co.Ref != "cs_1" || co.CustomerRef != "cus_1" {
		t.Fatalf("setup: %+v %v", co, err)
	}
	if strings.Join(api.reqs, ",") != "POST /v1/customers,POST /v1/checkout/sessions" || api.keys[0] != "payrudder-customer-0192-cust" || api.keys[1] != "setup-1" {
		t.Errorf("requests %v keys %v", api.reqs, api.keys)
	}
	if api.forms[0].Get("email") != "ada@example.com" || api.forms[0].Get("name") != "Ada" || api.forms[0].Get("metadata[payrudder_external_id]") != "user-42" {
		t.Errorf("customer form: %v", api.forms[0])
	}
	for k, v := range map[string]string{"mode": "setup", "customer": "cus_1", "currency": "eur", "success_url": "https://pay.example.com/back", "metadata[payrudder_customer_id]": "0192-cust", "metadata[payrudder_external_id]": "user-42"} {
		if api.forms[1].Get(k) != v {
			t.Errorf("session %s = %q, want %q", k, api.forms[1].Get(k), v)
		}
	}
	if api.forms[1].Get("line_items[0][quantity]") != "" || api.forms[1].Get("customer_creation") != "" {
		t.Errorf("a setup session takes no line item and needs no customer_creation: %v", api.forms[1])
	}
	req.CustomerRef = "cus_known"
	if co, err := c.CreateSetupCheckout(context.Background(), req); err != nil || co.CustomerRef != "cus_known" || len(api.reqs) != 3 || api.forms[2].Get("customer") != "cus_known" {
		t.Errorf("a known customer: %+v %v %v", co, err, api.reqs)
	}
	for _, bad := range []payments.SetupRequest{{}, {PayrudderCustomerID: "c", ExternalID: "e", Currency: "EUR", ReturnURL: "https://x"}, {PayrudderCustomerID: "c", ExternalID: "e", Currency: "EURO", ReturnURL: "https://x", IdempotencyKey: "k"}} {
		if _, err := c.CreateSetupCheckout(context.Background(), bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// The card is learned from the notification, never from the customer's browser: the completed setup session names the SetupIntent, which gives the payment method; what
// Charge takes is "customer:method". A SetupIntent that did not succeed, a session Payrudder did not make and a method on no customer are ignored.
func TestASavedCardIsLearnedFromTheSetupSession(t *testing.T) {
	now := time.Now()
	setup := func(meta string) string {
		return `{"id":"evt_s","type":"checkout.session.completed","created":1790000000,"data":{"object":{"id":"cs_1","mode":"setup","customer":"cus_1","setup_intent":"seti_1","payment_status":"no_payment_required","metadata":` + meta + `}}}`
	}
	ours := `{"payrudder_customer_id":"0192-cust","payrudder_external_id":"user-42"}`
	for _, c := range []struct {
		name, body, intent string
		saved              bool
	}{
		{"saved", setup(ours), `{"status":"succeeded","payment_method":"pm_9","customer":"cus_1"}`, true},
		{"the intent names the customer", strings.Replace(setup(ours), `"customer":"cus_1",`, "", 1), `{"status":"succeeded","payment_method":"pm_9","customer":"cus_7"}`, true},
		{"not succeeded", setup(ours), `{"status":"requires_payment_method","payment_method":"pm_9","customer":"cus_1"}`, false},
		{"no method", setup(ours), `{"status":"succeeded","customer":"cus_1"}`, false},
		{"not Payrudder's session", setup(`{}`), `{"status":"succeeded","payment_method":"pm_9","customer":"cus_1"}`, false},
		{"on no customer", strings.Replace(setup(ours), `"customer":"cus_1",`, "", 1), `{"status":"succeeded","payment_method":"pm_9"}`, false},
	} {
		api := newStripeAPI(t, map[string]string{"GET /v1/setup_intents/seti_1": c.intent})
		cl := api.client(t)
		cl.now = func() time.Time { return now }
		n, err := cl.ConfirmNotification(context.Background(), []byte(c.body), signed(cl, c.body, now))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !c.saved {
			if n.Kind != payments.KindIgnored {
				t.Errorf("%s: %+v, want ignored", c.name, n)
			}
			continue
		}
		wantCus := map[bool]string{true: "cus_7", false: "cus_1"}[strings.Contains(c.name, "names")]
		if n.Kind != payments.KindPaymentMethod || n.PaymentMethod == nil || n.PaymentMethod.Token != wantCus+":pm_9" || n.PaymentMethod.CustomerRef != wantCus ||
			n.PaymentMethod.ExternalID != "user-42" || n.PaymentMethod.PayrudderCustomerID != "0192-cust" || n.ProviderRef != "pm_9" || n.Ref.EventID != "evt_s" {
			t.Errorf("%s: %+v %+v", c.name, n, n.PaymentMethod)
		}
		if len(api.reqs) != 1 || api.reqs[0] != "GET /v1/setup_intents/seti_1" {
			t.Errorf("%s: requests %v", c.name, api.reqs)
		}
	}
	// A body that does not verify reaches no API at all.
	api := newStripeAPI(t, map[string]string{})
	cl := api.client(t)
	forged := http.Header{}
	forged.Set("Stripe-Signature", SignatureHeader("whsec_other", now, []byte(setup(ours))))
	if _, err := cl.ConfirmNotification(context.Background(), []byte(setup(ours)), forged); !errors.Is(err, payments.ErrUnverified) || len(api.reqs) != 0 {
		t.Errorf("forged: %v, requests %v", err, api.reqs)
	}
	// A card saved by a charge is the one the next charge takes: "cus_...:pm_..." is what Charge reads.
	chargeAPI := newFake(t, 200, `{"id":"pi_1","status":"succeeded","amount":500,"currency":"eur"}`)
	if _, err := chargeAPI.client(t).Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "i", AmountMinor: 500, Currency: "EUR", PaymentMethod: "cus_1:pm_9"}); err != nil ||
		chargeAPI.form.Get("customer") != "cus_1" || chargeAPI.form.Get("payment_method") != "pm_9" {
		t.Errorf("charge with the saved token: %v %v", err, chargeAPI.form)
	}
}

// The five dispute events become one notification about the case as the object says it now: the payment is the PaymentIntent (retrieved from the Charge when the dispute
// has none), inquiries are not chargebacks, and a status nobody knows is ignored rather than guessed at.
func TestDisputesAreNotifiedAsOneCase(t *testing.T) {
	f := newFake(t, 200, `{"id":"ch_1","payment_intent":"pi_from_charge"}`)
	c := f.client(t)
	now := time.Now()
	c.now = func() time.Time { return now }
	confirm := func(typ, object string) payments.Notification {
		t.Helper()
		body := `{"id":"evt_d","type":"` + typ + `","created":1782000000,"data":{"object":` + object + `}}`
		n, err := c.ConfirmNotification(context.Background(), []byte(body), signed(c, body, now))
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		return n
	}
	for _, typ := range WebhookEvents[len(WebhookEvents)-5:] {
		if !strings.HasPrefix(typ, "charge.dispute.") {
			t.Fatalf("the events the endpoint is created for do not end with the five dispute events: %v", WebhookEvents)
		}
	}
	opened := confirm("charge.dispute.created", `{"id":"du_1","object":"dispute","amount":1250,"currency":"eur","charge":"ch_1","payment_intent":"pi_1","reason":"fraudulent","status":"needs_response","evidence_details":{"due_by":1782864000}}`)
	d := opened.Dispute
	if opened.Kind != payments.KindDispute || opened.ProviderRef != "pi_1" || opened.AmountMinor != 1250 || opened.Currency != "EUR" || d == nil || d.ID != "du_1" ||
		d.Outcome != payments.DisputeOpen || !d.Formal || d.Reason != "fraudulent" || d.EvidenceDue.Unix() != 1782864000 || d.Status != "needs_response" {
		t.Fatalf("created = %+v %+v", opened, d)
	}
	if f.last != nil && strings.HasPrefix(f.last.URL.Path, "/v1/charges") {
		t.Error("the charge was retrieved though the dispute named the PaymentIntent")
	}
	// payment_intent is null: the Charge says which payment it is.
	viaCharge := confirm("charge.dispute.updated", `{"id":"du_2","amount":500,"currency":"usd","charge":"ch_1","payment_intent":null,"reason":"general","status":"under_review"}`)
	if viaCharge.ProviderRef != "pi_from_charge" || f.last.URL.Path != "/v1/charges/ch_1" || viaCharge.Dispute.Outcome != payments.DisputeOpen {
		t.Errorf("via the charge = %+v (asked %s)", viaCharge, f.last.URL.Path)
	}
	for status, want := range map[string]struct {
		outcome payments.DisputeOutcome
		formal  bool
	}{
		"needs_response": {payments.DisputeOpen, true}, "under_review": {payments.DisputeOpen, true}, "appeal_needs_response": {payments.DisputeOpen, true}, "appeal_under_review": {payments.DisputeOpen, true},
		"warning_needs_response": {payments.DisputeOpen, false}, "warning_under_review": {payments.DisputeOpen, false},
		"won": {payments.DisputeWon, true}, "lost": {payments.DisputeLost, true}, "warning_closed": {payments.DisputeWon, false}, "prevented": {payments.DisputeWon, false},
	} {
		n := confirm("charge.dispute.closed", `{"id":"du_3","amount":1,"currency":"usd","payment_intent":"pi_3","status":"`+status+`"}`)
		if n.Dispute == nil || n.Dispute.Outcome != want.outcome || n.Dispute.Formal != want.formal {
			t.Errorf("%s: %+v, want %s (formal %v)", status, n.Dispute, want.outcome, want.formal)
		}
	}
	for _, typ := range []string{"charge.dispute.funds_withdrawn", "charge.dispute.funds_reinstated"} {
		if n := confirm(typ, `{"id":"du_4","amount":1,"currency":"usd","payment_intent":"pi_4","status":"won"}`); n.Kind != payments.KindDispute || n.Dispute.Outcome != payments.DisputeWon {
			t.Errorf("%s: %+v", typ, n)
		}
	}
	if n := confirm("charge.dispute.updated", `{"id":"du_5","amount":1,"currency":"usd","payment_intent":"pi_5","status":"something_new"}`); n.Kind != payments.KindIgnored {
		t.Errorf("an unknown status: %+v", n)
	}
}
