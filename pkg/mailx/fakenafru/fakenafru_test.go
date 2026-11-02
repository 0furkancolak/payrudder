package fakenafru_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/mailx/fakenafru"
)

func TestFakeRecordsWhatNafruSends(t *testing.T) {
	f := fakenafru.New()
	defer f.Close()
	f.APIKey = "nk_live"
	s := mailx.Nafru{Resend: mailx.Resend{BaseURL: f.URL, APIKey: "nk_live"}}
	m := mailx.Message{From: "Acme <b@acme.test>", To: "ada@example.com", Subject: "Hi", Text: "t", ID: "k1", Template: "payment_failed.v1"}
	id, err := s.Send(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := s.Send(context.Background(), m) // the same Idempotency-Key is the same message
	got := f.Sent()
	if again != id || len(got) != 1 || got[0].Tags["payrudder_template"] != "payment_failed_v1" || got[0].Headers["X-Payrudder-Template"] != "payment_failed.v1" || got[0].To[0] != "ada@example.com" {
		t.Errorf("sent = %+v, ids %q %q", got, id, again)
	}
	var perm *mailx.PermanentError
	if _, err := (mailx.Nafru{Resend: mailx.Resend{BaseURL: f.URL, APIKey: "wrong"}}).Send(context.Background(), m); !errors.As(err, &perm) || perm.Status != 401 {
		t.Errorf("a wrong key: %v", err)
	}
	f.Fail(503)
	if _, err := s.Send(context.Background(), mailx.Message{From: "a@b.c", To: "x@y.z", Subject: "s", ID: "k2"}); err == nil || errors.As(err, &perm) {
		t.Errorf("an outage is a retry, not a refusal: %v", err)
	}
}

// What the fake posts is what the gateway verifies and the translation reads: a signed Resend-shaped webhook is a valid event for the notifier.
func TestResendWebhookRoundTrip(t *testing.T) {
	const secret = "whsec_dGhlLXN2aXgtc2VjcmV0LW9mLXRoZS10ZXN0cw=="
	var gotBody []byte
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeader = r.Header.Clone()
	}))
	defer srv.Close()
	body := fakenafru.ResendWebhook("email.bounced", "fake-1", "ada@example.com", "Permanent")
	if status, err := fakenafru.PostSigned(context.Background(), srv.URL, secret, body); err != nil || status != 200 {
		t.Fatalf("post: %d %v", status, err)
	}
	if err := mailx.VerifySvix(gotHeader, gotBody, secret, time.Now(), mailx.SvixTolerance); err != nil {
		t.Fatalf("what the fake signs does not verify: %v", err)
	}
	events, err := mailx.ParseResendWebhook(gotBody)
	if err != nil || len(events) != 1 || events[0].MessageID != "fake-1" || events[0].Recipient != "ada@example.com" || !events[0].Suppresses() || events[0].Validate() != nil {
		t.Fatalf("events %+v, %v", events, err)
	}
	if events, _ := mailx.ParseResendWebhook(fakenafru.ResendWebhook("email.bounced", "fake-2", "ada@example.com", "Temporary")); len(events) != 1 || events[0].Suppresses() {
		t.Errorf("a temporary bounce suppressed an address: %+v", events)
	}
}
