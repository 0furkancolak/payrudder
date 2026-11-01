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
	t.Run("a refusal is a decline in Payrudder's vocabulary, or carries the error group when iyzico's group has no equivalent", func(t *testing.T) {
		for body, want := range map[string]string{
			`{"status":"failure","errorCode":"10051","errorMessage":"Not sufficient funds","errorGroup":"NOT_SUFFICIENT_FUNDS"}`: "insufficient_funds",
			`{"status":"failure","errorCode":"10057","errorMessage":"restricted","errorGroup":"NOT_PERMITTED_TO_CARDHOLDER"}`:    "NOT_PERMITTED_TO_CARDHOLDER",
			`{"status":"failure","errorCode":"10999","errorMessage":"new"}`:                                                      "10999",
		} {
			f, c := newFake(t, nil)
			f.reply = func(string, map[string]any) (int, string) { return 200, body }
			_, err := c.Charge(context.Background(), req)
			var d *payments.DeclinedError
			if !errors.As(err, &d) || d.Code != want {
				t.Errorf("%s: err = %v, want a decline %q", body, err, want)
			}
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

func chargeWith(t *testing.T, body string, settings func(*Settings)) error {
	t.Helper()
	f, c := newFake(t, settings)
	f.reply = func(string, map[string]any) (int, string) { return 200, body }
	_, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "i", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct"})
	return err
}

// iyzico's fraud filters: "ship only when the value is 1; for 0, wait for notification" (create-payment, retrieve, CF-Retrieve; read 2026-11-02).
func TestAPaymentUnderFraudReviewIsNotPaid(t *testing.T) {
	var p *payments.PendingError
	var d *payments.DeclinedError
	if err := chargeWith(t, `{"status":"success","paymentId":"9","fraudStatus":0}`, nil); !errors.As(err, &p) || p.Ref != "9" {
		t.Errorf("fraudStatus 0 = %v; want a pending payment, not a success", err)
	}
	if err := chargeWith(t, `{"status":"success","paymentId":"9","fraudStatus":-1}`, nil); !errors.As(err, &d) || d.Code != "fraudulent" {
		t.Errorf("fraudStatus -1 = %v; want a decline", err)
	}
	for _, body := range []string{`{"status":"success","paymentId":"9","fraudStatus":1}`, `{"status":"success","paymentId":"9"}`} {
		if err := chargeWith(t, body, nil); err != nil {
			t.Errorf("%s = %v; want a paid charge", body, err)
		}
	}

	// A lookup says the same.
	for body, check := range map[string]func(payments.StatusResult, error) bool{
		`{"status":"success","paymentId":"1","paymentStatus":"SUCCESS","fraudStatus":0}`: func(_ payments.StatusResult, err error) bool {
			var p *payments.PendingError
			return errors.As(err, &p)
		},
		`{"status":"success","paymentId":"1","paymentStatus":"SUCCESS","fraudStatus":-1}`: func(r payments.StatusResult, err error) bool {
			return err == nil && r.State == payments.StateDeclined && r.DeclineCode == "fraudulent"
		},
		`{"status":"success","paymentId":"1","paymentStatus":"SUCCESS","fraudStatus":1}`: func(r payments.StatusResult, err error) bool {
			return err == nil && r.State == payments.StateSucceeded
		},
	} {
		f, c := newFake(t, nil)
		f.reply = func(string, map[string]any) (int, string) { return 200, body }
		if got, err := c.Status(context.Background(), "k"); !check(got, err) {
			t.Errorf("Status %s = %+v, %v", body, got, err)
		}
	}
}

func TestErrorClassification(t *testing.T) {
	kind := func(body string) string {
		err := chargeWith(t, body, nil)
		var tr *payments.TransientError
		var d *payments.DeclinedError
		var e *Error
		switch {
		case errors.As(err, &tr):
			return "transient"
		case errors.As(err, &d):
			return "decline " + d.Code
		case errors.As(err, &e):
			return "request error " + e.Code
		}
		return "other"
	}
	for body, want := range map[string]string{
		// the limiter's own example has the code as a number
		`{"status":"failure","errorCode":50000,"errorMessage":"Request Limit Exceeded","locale":"en"}`: "transient",
		// the bank could not answer: "please try again later"
		`{"status":"failure","errorCode":"10214","errorGroup":"COMMUNICATION_OR_SYSTEM_ERROR"}`: "transient",
		`{"status":"failure","errorCode":"10219","errorGroup":"REQUEST_TIMEOUT"}`:               "transient",
		`{"status":"failure","errorCode":"1","errorMessage":"System error occurred"}`:           "transient",
		// the table marks every request error "retry: true", which must not make a wrong signature or a missing field retried as if it were a flaky bank
		`{"status":"failure","errorCode":"1000","errorMessage":"Invalid signature","retryable":true}`:        "request error 1000",
		`{"status":"failure","errorCode":"5111","errorMessage":"CardUserKey must be sent","retryable":true}`: "request error 5111",
		`{"status":"failure","errorCode":"12","errorMessage":"Invalid card number","retryable":true}`:        "request error 12",
		// the card: the issuer's reasons in Payrudder's words
		`{"status":"failure","errorCode":"10054","errorGroup":"EXPIRED_CARD"}`:                                "decline expired_card",
		`{"status":"failure","errorCode":"10217","errorGroup":"DEBIT_CARDS_REQUIRES_3DS"}`:                    "decline authentication_required",
		`{"status":"failure","errorCode":"6001","errorMessage":"Payment request has not passed Fraud check"}`: "decline fraudulent",
		// a code nobody documented: retryable decides, else it is a decline as iyzico spells it
		`{"status":"failure","errorCode":"77777","retryable":true}`: "transient",
		`{"status":"failure","errorCode":"77777"}`:                  "decline 77777",
	} {
		if got := kind(body); got != want {
			t.Errorf("%s = %s, want %s", body, got, want)
		}
	}
}

func TestHealthCheckAsksForAnEightDigitBINAndReadsTheAnswer(t *testing.T) {
	check := func(body string) error {
		f, c := newFake(t, nil)
		f.reply = func(path string, in map[string]any) (int, string) {
			if bin, _ := in["binNumber"].(string); len(bin) != 8 {
				t.Errorf("binNumber = %q; the BIN service takes 8 digits", bin)
			}
			return 200, body
		}
		return c.CheckHealth(context.Background())
	}
	if err := check(`{"status":"success","binNumber":"55287900"}`); err != nil {
		t.Errorf("a found BIN = %v", err)
	}
	// The request was accepted: the BIN just is not known to iyzico. That says nothing against the credentials.
	for _, code := range []string{"5066", "10213"} {
		if err := check(`{"status":"failure","errorCode":"` + code + `","errorMessage":"Bin is not found"}`); err != nil {
			t.Errorf("BIN not found (%s) = %v; the credentials worked", code, err)
		}
	}
	var e *Error
	if err := check(`{"status":"failure","errorCode":"1000","errorMessage":"Invalid signature"}`); !errors.As(err, &e) || e.Code != "1000" {
		t.Errorf("a wrong signature = %v", err)
	}
	// The BIN service is limited to 50 a minute: over it is "could not be asked", not "bad credentials".
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"failure","errorCode":50000,"errorMessage":"Request Limit Exceeded"}`
	}
	if _, err := payments.Check(context.Background(), c); !errors.Is(err, payments.ErrUnreachable) {
		t.Errorf("over the limit = %v; want ErrUnreachable", err)
	}
	if _, err := payments.Check(context.Background(), func() payments.Provider {
		f2, c2 := newFake(t, nil)
		f2.reply = func(string, map[string]any) (int, string) { return 200, `{"status":"failure","errorCode":"1000"}` }
		return c2
	}()); !errors.Is(err, payments.ErrBadCredentials) {
		t.Errorf("a wrong signature = %v; want ErrBadCredentials", err)
	}
}

// The checkout form's notification names a token and an iyziPaymentId and has no paymentId.
const hostedNotification = `{"iyziEventTime":1700000000,"iyziEventType":"CHECKOUT_FORM_AUTH","iyziReferenceCode":"ref-h","iyziPaymentId":987654321,"token":"tok-1","paymentConversationId":"link:inv-1:1","merchantId":"m","status":"SUCCESS"}`

func TestAHostedPageNotificationIsParsedAndConfirmedByToken(t *testing.T) {
	e, err := ParseWebhook([]byte(hostedNotification))
	if err != nil || !e.Hosted() || e.Token != "tok-1" || e.ID() != "987654321" || e.Reference != "ref-h" {
		t.Fatalf("event = %+v, %v", e, err)
	}
	ref, err := ParseNotification([]byte(hostedNotification), nil)
	if err != nil || ref.EventID != "ref-h" || ref.IdempotencyKey != "link:inv-1:1" {
		t.Fatalf("ref = %+v, %v", ref, err)
	}
	if _, err := ParseWebhook([]byte(`{"iyziReferenceCode":"r","status":"SUCCESS"}`)); err == nil {
		t.Error("neither a paymentId nor a token")
	}

	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentStatus":"SUCCESS","paymentId":"987654321","basketId":"inv-1","token":"tok-1","fraudStatus":1}`
	}
	n, err := c.ConfirmNotification(context.Background(), []byte(hostedNotification), nil)
	if err != nil || n.State != payments.StateSucceeded || n.ProviderRef != "987654321" || n.Ref.IdempotencyKey != "link:inv-1:1" || n.Ref.EventID != "ref-h" {
		t.Fatalf("ConfirmNotification = %+v, %v", n, err)
	}
	if f.paths[0] != "/payment/iyzipos/checkoutform/auth/ecom/detail" || f.last["token"] != "tok-1" || f.last["paymentId"] != nil {
		t.Errorf("the retrieve call = %s %v; want CF-Retrieve by token", f.paths[0], f.last)
	}

	// What iyzico answers decides, not the body: a payment of another invoice, or of another token, is not believed.
	for name, reply := range map[string]string{
		"another invoice": `{"status":"success","paymentStatus":"SUCCESS","paymentId":"1","basketId":"inv-2","token":"tok-1"}`,
		"another token":   `{"status":"success","paymentStatus":"SUCCESS","paymentId":"1","basketId":"inv-1","token":"tok-other"}`,
	} {
		f.reply = func(string, map[string]any) (int, string) { return 200, reply }
		if _, err := c.ConfirmNotification(context.Background(), []byte(hostedNotification), nil); !errors.Is(err, payments.ErrUnverified) {
			t.Errorf("%s: %v, want ErrUnverified", name, err)
		}
	}
	// A hosted payment held for review is not paid yet.
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentStatus":"SUCCESS","paymentId":"1","basketId":"inv-1","token":"tok-1","fraudStatus":0}`
	}
	var p *payments.PendingError
	if _, err := c.ConfirmNotification(context.Background(), []byte(hostedNotification), nil); !errors.As(err, &p) {
		t.Errorf("fraudStatus 0 on a hosted payment = %v; want pending", err)
	}
}

// The signature is the hex HMAC-SHA256, under the secret key, of the secret key + iyziEventType + paymentId + paymentConversationId + status (direct format) or
// secret key + iyziEventType + iyziPaymentId + token + paymentConversationId + status (hosted format), as docs.iyzico.com/en/advanced/webhook spells it.
func TestNotificationSignatureV3(t *testing.T) {
	sign := func(msg string) http.Header {
		mac := hmac.New(sha256.New, []byte("sec"))
		mac.Write([]byte(msg))
		h := http.Header{}
		h.Set("X-IYZ-SIGNATURE-V3", hex.EncodeToString(mac.Sum(nil)))
		return h
	}
	direct := `{"iyziEventTime":1700000000,"iyziEventType":"API_AUTH","iyziReferenceCode":"ref-1","paymentId":23456,"paymentConversationId":"inv-1:charge:0","status":"SUCCESS"}`
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"23456","paymentStatus":"SUCCESS","basketId":"inv-1","token":"tok-1"}`
	}
	good := map[string]struct {
		body string
		h    http.Header
	}{
		"direct": {direct, sign("secAPI_AUTH23456inv-1:charge:0SUCCESS")},
		"hosted": {hostedNotification, sign("secCHECKOUT_FORM_AUTH987654321tok-1link:inv-1:1SUCCESS")},
	}
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"23456","paymentStatus":"SUCCESS","basketId":"inv-1","token":"tok-1"}`
	}
	for name, tc := range good {
		if _, err := c.ConfirmNotification(context.Background(), []byte(tc.body), tc.h); err != nil {
			t.Errorf("%s with a valid signature: %v", name, err)
		}
		// the same signature over another status is a forged notification
		tampered := strings.Replace(tc.body, `"SUCCESS"`, `"FAILURE"`, 1)
		if _, err := c.ConfirmNotification(context.Background(), []byte(tampered), tc.h); !errors.Is(err, payments.ErrUnverified) {
			t.Errorf("%s with a signature that does not match: %v, want ErrUnverified", name, err)
		}
	}
	// No header: fine while the account has not turned the feature on, refused once it says iyzico signs.
	if _, err := c.ConfirmNotification(context.Background(), []byte(direct), nil); err != nil {
		t.Errorf("no header, signature not required: %v", err)
	}
	f2, strict := newFake(t, func(s *Settings) { s.RequireWebhookSignature = true })
	f2.reply = f.reply
	if _, err := strict.ConfirmNotification(context.Background(), []byte(direct), nil); !errors.Is(err, payments.ErrUnverified) {
		t.Errorf("no header, signature required: %v, want ErrUnverified", err)
	}
	if _, err := strict.ConfirmNotification(context.Background(), []byte(direct), good["direct"].h); err != nil {
		t.Errorf("signed, signature required: %v", err)
	}
	if len(f2.paths) != 1 {
		t.Errorf("%d lookups; a notification refused for its signature must not reach iyzico", len(f2.paths))
	}
}
