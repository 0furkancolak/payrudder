package payrudder

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"
)

const testSecret = "whsec_test_secret"

var testBody = []byte(`{"id":"0192","type":"invoice.paid","created":"2026-10-20T10:00:00Z","tenant_id":"t1","data":{"invoice_id":"i1","number":"INV-2026-000007","total_minor":"1116","tax_minor":"186","net_minor":"930","environment":"live","status":"paid"}}`)

func sign(secret string, ts int64, payload []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(m, "%d.", ts)
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}

func header(ts int64, sigs ...string) string {
	h := fmt.Sprintf("t=%d", ts)
	for _, s := range sigs {
		h += ",v1=" + s
	}
	return h
}

var now = time.Unix(1_700_000_000, 0)

func TestVerifyWebhook(t *testing.T) {
	ts := now.Unix()
	good := sign(testSecret, ts, testBody)
	tampered := append([]byte(nil), testBody...)
	tampered[len(tampered)-5] = 'X'

	cases := []struct {
		name    string
		payload []byte
		header  string
		secret  string
		tol     time.Duration
		at      time.Time
		want    error
	}{
		{"valid", testBody, header(ts, good), testSecret, 0, now, nil},
		{"valid a minute apart", testBody, header(ts, good), testSecret, 0, now.Add(time.Minute), nil},
		{"tampered payload", tampered, header(ts, good), testSecret, 0, now, ErrWebhookSignature},
		{"wrong secret", testBody, header(ts, good), "whsec_other", 0, now, ErrWebhookSignature},
		{"stale", testBody, header(ts, good), testSecret, 0, now.Add(6 * time.Minute), ErrWebhookTimestamp},
		{"from the future", testBody, header(ts, good), testSecret, 0, now.Add(-6 * time.Minute), ErrWebhookTimestamp},
		{"custom tolerance allows", testBody, header(ts, good), testSecret, time.Hour, now.Add(30 * time.Minute), nil},
		{"custom tolerance refuses", testBody, header(ts, good), testSecret, time.Minute, now.Add(2 * time.Minute), ErrWebhookTimestamp},
		{"timestamp is signed", testBody, header(ts+1, good), testSecret, 0, now, ErrWebhookSignature},
		{"rotated: old secret second", testBody, header(ts, sign("whsec_new", ts, testBody), good), testSecret, 0, now, nil},
		{"rotated: old secret first", testBody, header(ts, good, sign("whsec_new", ts, testBody)), testSecret, 0, now, nil},
		{"rotated: new secret", testBody, header(ts, good, sign("whsec_new", ts, testBody)), "whsec_new", 0, now, nil},
		{"rotated: neither", testBody, header(ts, sign("a", ts, testBody), sign("b", ts, testBody)), testSecret, 0, now, ErrWebhookSignature},
		{"garbage v1 is skipped", testBody, "t=" + fmt.Sprint(ts) + ",v1=zz," + "v1=" + good, testSecret, 0, now, nil},
		{"spaces after commas", testBody, fmt.Sprintf("t=%d, v1=%s", ts, good), testSecret, 0, now, nil},
		{"empty header", testBody, "", testSecret, 0, now, ErrWebhookHeader},
		{"no v1", testBody, fmt.Sprintf("t=%d", ts), testSecret, 0, now, ErrWebhookHeader},
		{"no t", testBody, "v1=" + good, testSecret, 0, now, ErrWebhookHeader},
		{"bad t", testBody, "t=abc,v1=" + good, testSecret, 0, now, ErrWebhookHeader},
		{"truncated signature", testBody, header(ts, good[:20]), testSecret, 0, now, ErrWebhookSignature},
		{"empty payload", nil, header(ts, good), testSecret, 0, now, ErrWebhookSignature},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyWebhookAt(tc.payload, tc.header, tc.secret, tc.tol, tc.at)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// The example of docs/16-webhooks.md computed by hand: the same bytes as the reference implementations sign.
func TestVerifyWebhookKnownVector(t *testing.T) {
	m := hmac.New(sha256.New, []byte("secret"))
	m.Write([]byte("1700000000.{}"))
	want := hex.EncodeToString(m.Sum(nil))
	if err := VerifyWebhookAt([]byte("{}"), "t=1700000000,v1="+want, "secret", 0, now); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyWebhookUsesTheRealClock(t *testing.T) {
	ts := time.Now().Unix()
	if err := VerifyWebhook(testBody, header(ts, sign(testSecret, ts, testBody)), testSecret, 0); err != nil {
		t.Fatal(err)
	}
}

func TestConstructEvent(t *testing.T) {
	ts := time.Now().Unix()
	h := header(ts, sign(testSecret, ts, testBody))
	ev, err := ConstructEvent(testBody, h, testSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "0192" || ev.Type != EventInvoicePaid || ev.TenantID != "t1" || ev.Created.Year() != 2026 {
		t.Fatalf("%+v", ev)
	}
	var d InvoiceEventData
	if err := ev.DecodeData(&d); err != nil {
		t.Fatal(err)
	}
	if d.TotalMinor != 1116 || d.TaxMinor != 186 || d.NetMinor != 930 || d.Environment != "live" || d.Number != "INV-2026-000007" {
		t.Fatalf("%+v", d)
	}

	if _, err := ConstructEvent(append([]byte(" "), testBody...), h, testSecret, 0); !errors.Is(err, ErrWebhookSignature) {
		t.Fatalf("modified body accepted: %v", err)
	}
	bad := []byte(`not json`)
	if _, err := ConstructEvent(bad, header(ts, sign(testSecret, ts, bad)), testSecret, 0); err == nil || errors.Is(err, ErrWebhookSignature) {
		t.Fatalf("signed garbage: %v", err)
	}
}

func TestEventTypeList(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range WebhookEventTypes {
		if seen[e] {
			t.Errorf("%s twice", e)
		}
		seen[e] = true
	}
	if len(seen) != 22 || !seen["credits.low"] || !seen["subscription.trial_will_end"] {
		t.Fatalf("%d types", len(seen))
	}
}
