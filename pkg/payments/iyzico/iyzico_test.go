package iyzico

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

func TestAuthorizationMatchesTheDocumentedFormat(t *testing.T) {
	// The documentation's example header decodes to apiKey:…&randomKey:123456789&signature:<64 hex>.
	header := Authorization("sandbox-key", "secret", "123456789", "/payment/bin/check", []byte(`{"binNumber":"535805"}`))
	if !strings.HasPrefix(header, "IYZWSv2 ") {
		t.Fatalf("header = %q", header)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "IYZWSv2 "))
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(`123456789/payment/bin/check{"binNumber":"535805"}`))
	want := "apiKey:sandbox-key&randomKey:123456789&signature:" + hex.EncodeToString(mac.Sum(nil))
	if string(raw) != want {
		t.Fatalf("decoded = %q\nwant      %q", raw, want)
	}
	// An empty body signs randomKey + path.
	empty := Authorization("k", "s", "1", "/p", nil)
	m2 := hmac.New(sha256.New, []byte("s"))
	m2.Write([]byte("1/p"))
	rawEmpty, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(empty, "IYZWSv2 "))
	if !strings.HasSuffix(string(rawEmpty), hex.EncodeToString(m2.Sum(nil))) {
		t.Errorf("empty body signature wrong: %s", rawEmpty)
	}
}

func TestPrice(t *testing.T) {
	for _, c := range []struct {
		minor    int64
		currency string
		want     string
	}{{1116, "TRY", "11.16"}, {5, "TRY", "0.05"}, {0, "USD", "0.00"}, {1116, "JPY", "1116"}, {1000, "KWD", "1.000"}, {100, "EUR", "1.00"}} {
		got, err := price(c.minor, c.currency)
		if err != nil || got != c.want {
			t.Errorf("price(%d, %s) = %q, %v; want %q", c.minor, c.currency, got, err, c.want)
		}
	}
	if _, err := price(1, "XXX"); err == nil {
		t.Error("unsupported currency must fail")
	}
	if _, err := price(-1, "TRY"); err == nil {
		t.Error("negative amount must fail")
	}
}

type fake struct {
	srv   *httptest.Server
	paths []string
	last  map[string]any
	reply func(path string, body map[string]any) (int, string)
}

func newFake(t *testing.T, settings func(*Settings)) (*fake, *Client) {
	t.Helper()
	f := &fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		// The request must be signed exactly as the documentation says.
		rnd := r.Header.Get("x-iyzi-rnd")
		if got, want := r.Header.Get("Authorization"), Authorization("api", "sec", rnd, r.URL.Path, raw); got != want {
			t.Errorf("bad authorization on %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.paths = append(f.paths, r.URL.Path)
		f.last = body
		code, out := f.reply(r.URL.Path, body)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(f.srv.Close)
	s := Settings{APIKey: "api", SecretKey: "sec", BaseURL: f.srv.URL, Buyer: Buyer{Name: "Ada", Surname: "Lovelace", IdentityNumber: "11111111111",
		Address: "Nisantasi", City: "Istanbul", Country: "Turkey", ZipCode: "34000", IP: "85.34.78.112", Email: "billing@example.com"}}
	if settings != nil {
		settings(&s)
	}
	c, err := New(s, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestCharge(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"23456","conversationId":"key-1","price":11.16}`
	}
	res, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "key-1", InvoiceID: "inv-1", AmountMinor: 1116, Currency: "TRY", PaymentMethod: "ck:ct"})
	if err != nil || res.ChargeID != "23456" {
		t.Fatalf("Charge = %+v, %v", res, err)
	}
	if f.paths[0] != "/payment/auth" {
		t.Errorf("path = %s", f.paths[0])
	}
	b := f.last
	if b["price"] != "11.16" || b["paidPrice"] != "11.16" || b["currency"] != "TRY" || b["conversationId"] != "key-1" || b["basketId"] != "inv-1" {
		t.Errorf("request = %v", b)
	}
	card := b["paymentCard"].(map[string]any)
	if card["cardUserKey"] != "ck" || card["cardToken"] != "ct" {
		t.Errorf("paymentCard = %v", card)
	}
	if b["buyer"].(map[string]any)["email"] != "billing@example.com" {
		t.Errorf("buyer = %v", b["buyer"])
	}
}

func TestChargeFailures(t *testing.T) {
	req := payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "i", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct"}
	t.Run("a refusal is a decline carrying the error group", func(t *testing.T) {
		f, c := newFake(t, nil)
		f.reply = func(string, map[string]any) (int, string) {
			return 200, `{"status":"failure","errorCode":"10051","errorMessage":"Not sufficient funds","errorGroup":"NOT_SUFFICIENT_FUNDS"}`
		}
		_, err := c.Charge(context.Background(), req)
		var d *payments.DeclinedError
		if !errors.As(err, &d) || d.Code != "NOT_SUFFICIENT_FUNDS" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a configured system error code is retried", func(t *testing.T) {
		f, c := newFake(t, func(s *Settings) { s.SystemErrorCodes = []string{"5"} })
		f.reply = func(string, map[string]any) (int, string) {
			return 200, `{"status":"failure","errorCode":"5","errorMessage":"system error"}`
		}
		_, err := c.Charge(context.Background(), req)
		var tr *payments.TransientError
		if !errors.As(err, &tr) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a server error and a rate limit are transient", func(t *testing.T) {
		for _, code := range []int{500, 502, 429} {
			f, c := newFake(t, nil)
			f.reply = func(string, map[string]any) (int, string) { return code, `{}` }
			_, err := c.Charge(context.Background(), req)
			var tr *payments.TransientError
			if !errors.As(err, &tr) {
				t.Errorf("status %d: err = %v", code, err)
			}
		}
	})
	t.Run("a payment method that is not a stored card is refused before any request", func(t *testing.T) {
		f, c := newFake(t, nil)
		r := req
		r.PaymentMethod = "pm_sandbox_success"
		if _, err := c.Charge(context.Background(), r); err == nil {
			t.Fatal("want an error")
		}
		if len(f.paths) != 0 {
			t.Error("a request was sent")
		}
	})
	t.Run("a missing idempotency key is refused", func(t *testing.T) {
		_, c := newFake(t, nil)
		r := req
		r.IdempotencyKey = ""
		if _, err := c.Charge(context.Background(), r); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestRefund(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"23456","refundHostReference":"ref-9"}`
	}
	res, err := c.Refund(context.Background(), payments.RefundRequest{IdempotencyKey: "r-1", ChargeID: "23456", AmountMinor: 1116, Currency: "TRY"})
	if err != nil || res.RefundID != "ref-9" {
		t.Fatalf("Refund = %+v, %v", res, err)
	}
	if f.paths[0] != "/v2/payment/refund" || f.last["paymentId"] != "23456" || f.last["price"] != "11.16" {
		t.Errorf("request %s %v", f.paths[0], f.last)
	}
	if _, err := c.Refund(context.Background(), payments.RefundRequest{AmountMinor: 1, Currency: "TRY"}); err == nil {
		t.Error("a refund without a charge id must fail")
	}
}

func TestStatus(t *testing.T) {
	t.Run("succeeded and declined", func(t *testing.T) {
		for body, want := range map[string]payments.State{
			`{"status":"success","paymentId":"1","paymentStatus":"SUCCESS"}`: payments.StateSucceeded,
			`{"status":"success","paymentId":"1","paymentStatus":"FAILURE"}`: payments.StateDeclined,
		} {
			f, c := newFake(t, nil)
			f.reply = func(string, map[string]any) (int, string) { return 200, body }
			got, err := c.Status(context.Background(), "key-1")
			if err != nil || got.State != want || got.ChargeID != "1" {
				t.Errorf("%s: %+v, %v", body, got, err)
			}
			if f.last["paymentConversationId"] != "key-1" {
				t.Errorf("request = %v", f.last)
			}
		}
	})
	t.Run("an unknown failure is an error, never not-found", func(t *testing.T) {
		f, c := newFake(t, nil)
		f.reply = func(string, map[string]any) (int, string) {
			return 200, `{"status":"failure","errorCode":"12345","errorMessage":"whatever"}`
		}
		got, err := c.Status(context.Background(), "k")
		if err == nil || got.State == payments.StateNotFound {
			t.Fatalf("got %+v, %v; a lookup that failed for an unknown reason must not allow a second charge", got, err)
		}
	})
	t.Run("a configured not-found code is not found", func(t *testing.T) {
		f, c := newFake(t, func(s *Settings) { s.NotFoundCodes = []string{"12345"} })
		f.reply = func(string, map[string]any) (int, string) { return 200, `{"status":"failure","errorCode":"12345"}` }
		got, err := c.Status(context.Background(), "k")
		if err != nil || got.State != payments.StateNotFound {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("a payment that is not final is an error", func(t *testing.T) {
		f, c := newFake(t, nil)
		f.reply = func(string, map[string]any) (int, string) {
			return 200, `{"status":"success","paymentId":"1","paymentStatus":"INIT_THREEDS"}`
		}
		if _, err := c.Status(context.Background(), "k"); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestWebhookParseAndConfirm(t *testing.T) {
	e, err := ParseWebhook([]byte(`{"iyziEventTime":1700000000,"iyziEventType":"API_AUTH","iyziReferenceCode":"ref-1","paymentId":23456,"paymentConversationId":"inv-1:charge:0","status":"SUCCESS"}`))
	if err != nil || e.ID() != "23456" || e.Reference != "ref-1" || e.Status != "SUCCESS" {
		t.Fatalf("event = %+v, %v", e, err)
	}
	if _, err := ParseWebhook([]byte(`{"status":"SUCCESS"}`)); err == nil {
		t.Error("a notification without reference and payment id must be refused")
	}
	if _, err := ParseWebhook([]byte(`nope`)); err == nil {
		t.Error("garbage must be refused")
	}

	f, c := newFake(t, nil)
	// A forged notification claims success; iyzico says the payment failed. Payrudder believes iyzico.
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"23456","paymentStatus":"FAILURE","basketId":"inv-1"}`
	}
	got, err := c.Confirm(context.Background(), e)
	if err != nil || got.State != payments.StateDeclined {
		t.Fatalf("Confirm = %+v, %v; the retrieved state must win over the notified one", got, err)
	}
	if f.last["paymentId"] != "23456" {
		t.Errorf("confirm request = %v", f.last)
	}
}

func TestFactoryAndCapabilities(t *testing.T) {
	p, err := Factory([]byte(`{"api_key":"a","secret_key":"s","base_url":"https://sandbox-api.iyzipay.com/"}`))
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if p.Name() != "iyzico" || caps.Owner() != payments.OwnerPayrudder || !caps.CanCharge("TRY") || caps.CanCharge("JPY") || !caps.Refunds {
		t.Errorf("capabilities = %+v", caps)
	}
	if _, err := Factory([]byte(`{"api_key":"a"}`)); err == nil {
		t.Error("a missing secret key must fail")
	}
	if _, err := Factory([]byte(`{`)); err == nil {
		t.Error("garbage settings must fail")
	}
}

func TestNotifier(t *testing.T) {
	r := payments.NewRegistry()
	Register(r)
	parse, ok := r.Parser(Name)
	if !ok {
		t.Fatal("no parser registered")
	}
	ref, err := parse([]byte(`{"iyziEventType":"API_AUTH","iyziReferenceCode":"ref-1","paymentId":7,"paymentConversationId":"key-1","status":"SUCCESS"}`), nil)
	if err != nil || ref.EventID != "ref-1" || ref.IdempotencyKey != "key-1" {
		t.Fatalf("ref = %+v, %v", ref, err)
	}
	if _, err := parse([]byte(`{"iyziReferenceCode":"ref-1","paymentId":7}`), nil); err == nil {
		t.Error("a notification that names no charge must be refused")
	}
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"7","paymentStatus":"SUCCESS","basketId":"inv-1"}`
	}
	var n payments.Notifier = c
	got, err := n.ConfirmNotification(context.Background(), []byte(`{"iyziReferenceCode":"ref-1","paymentId":7,"paymentConversationId":"inv-1:charge:0","status":"FAILURE"}`), nil)
	if err != nil || got.State != payments.StateSucceeded || got.ProviderRef != "7" || got.Ref.IdempotencyKey != "inv-1:charge:0" {
		t.Fatalf("ConfirmNotification = %+v, %v; the provider's answer, not the body's status, decides", got, err)
	}
}

// The webhook is unsigned: its conversation id is a claim. A real payment of a small invoice must not confirm the charge of a big one.
func TestConfirmRefusesAPaymentOfAnotherInvoice(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"7","paymentStatus":"SUCCESS","basketId":"small-invoice"}`
	}
	body := []byte(`{"iyziReferenceCode":"ref-1","paymentId":7,"paymentConversationId":"big-invoice:charge:0","status":"SUCCESS"}`)
	if got, err := c.ConfirmNotification(context.Background(), body, nil); !errors.Is(err, payments.ErrUnverified) {
		t.Fatalf("ConfirmNotification = %+v, %v; want ErrUnverified for a payment that belongs to another invoice", got, err)
	}
	for name, reply := range map[string]string{
		"no basket id in the answer":  `{"status":"success","paymentId":"7","paymentStatus":"SUCCESS"}`,
		"a basket of another invoice": `{"status":"success","paymentId":"7","paymentStatus":"SUCCESS","basketId":"big-invoice-2"}`,
	} {
		f.reply = func(string, map[string]any) (int, string) { return 200, reply }
		if _, err := c.ConfirmNotification(context.Background(), body, nil); !errors.Is(err, payments.ErrUnverified) {
			t.Errorf("%s: %v, want ErrUnverified", name, err)
		}
	}
	// A conversation id that is not one of Payrudder's charge keys names no invoice.
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"7","paymentStatus":"SUCCESS","basketId":"big-invoice"}`
	}
	if _, err := c.ConfirmNotification(context.Background(), []byte(`{"iyziReferenceCode":"ref-1","paymentId":7,"paymentConversationId":"whatever","status":"SUCCESS"}`), nil); !errors.Is(err, payments.ErrUnverified) {
		t.Errorf("a conversation id without an invoice: %v, want ErrUnverified", err)
	}
}

// An answer that does not say the payment succeeded is not a success: an empty status, or a success without a payment id (it could never be refunded).
func TestAnUnstatedPaymentIsNotASuccess(t *testing.T) {
	for name, reply := range map[string]string{
		"empty status":           `{"status":"success","paymentId":"1","basketId":"inv-1"}`,
		"success without an id":  `{"status":"success","paymentStatus":"SUCCESS","basketId":"inv-1"}`,
		"empty status, no id":    `{"status":"success","basketId":"inv-1"}`,
		"a status nobody speaks": `{"status":"success","paymentId":"1","paymentStatus":"WAITING","basketId":"inv-1"}`,
	} {
		f, c := newFake(t, nil)
		f.reply = func(string, map[string]any) (int, string) { return 200, reply }
		if got, err := c.Status(context.Background(), "inv-1:charge:0"); err == nil {
			t.Errorf("Status, %s: %+v, want an error", name, got)
		}
		e := WebhookEvent{Reference: "r", PaymentID: "1", ConversationID: "inv-1:charge:0"}
		if got, err := c.Confirm(context.Background(), e); err == nil {
			t.Errorf("Confirm, %s: %+v, want an error", name, got)
		}
	}
}

// A payment on a hosted page has the key "link:<invoice>:<attempt>": its invoice is the second part, not "link".
func TestConfirmAcceptsAPaymentOfAHostedPageKey(t *testing.T) {
	for _, key := range []string{"link:inv-1:3", "link:inv-1:i:iyzico", "inv-1:charge:0"} {
		f, c := newFake(t, nil)
		f.reply = func(string, map[string]any) (int, string) {
			return 200, `{"status":"success","paymentId":"7","paymentStatus":"SUCCESS","basketId":"inv-1"}`
		}
		got, err := c.Confirm(context.Background(), WebhookEvent{PaymentID: "7", ConversationID: key, Status: "SUCCESS"})
		if err != nil || got.State != payments.StateSucceeded {
			t.Errorf("%s: Confirm = %+v, %v; want a confirmed payment of inv-1", key, got, err)
		}
		// The same key with the basket of another invoice is still refused.
		f.reply = func(string, map[string]any) (int, string) {
			return 200, `{"status":"success","paymentId":"7","paymentStatus":"SUCCESS","basketId":"inv-2"}`
		}
		if _, err := c.Confirm(context.Background(), WebhookEvent{PaymentID: "7", ConversationID: key, Status: "SUCCESS"}); !errors.Is(err, payments.ErrUnverified) {
			t.Errorf("%s: a payment of another invoice must be unverified, got %v", key, err)
		}
	}
	for _, key := range []string{"", "link:", "link::1", "nocolon"} {
		if _, ok := invoiceOfKey(key); ok {
			t.Errorf("%q is not a key of a charge", key)
		}
	}
}
