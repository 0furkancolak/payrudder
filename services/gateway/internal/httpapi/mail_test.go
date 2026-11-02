package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/services/gateway/internal/mailadmin"
)

// fakeMail records what the gateway asks of the notifier and answers from fixtures.
type fakeMail struct {
	tenant  uuid.UUID
	after   string
	size    int
	removed string
	preview mailadmin.PriceChangePreview
	events  []mailx.Event
	err     error
	// eventErr, when set, is what handing over an event answers.
	eventErr error
}

func (f *fakeMail) Event(_ context.Context, e mailx.Event) error {
	f.events = append(f.events, e)
	return f.eventErr
}

func (f *fakeMail) Suppressions(_ context.Context, t uuid.UUID, after string, size int) ([]mailadmin.Suppression, string, error) {
	f.tenant, f.after, f.size = t, after, size
	return []mailadmin.Suppression{{Address: "ada@example.com", Reason: "bounce", Detail: "550", CreateTime: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}}, "ada@example.com", f.err
}

func (f *fakeMail) Unsuppress(_ context.Context, t uuid.UUID, a string) error {
	f.tenant, f.removed = t, a
	return f.err
}

func (f *fakeMail) Messages(_ context.Context, t uuid.UUID, after string, size int) ([]mailadmin.Message, string, error) {
	f.tenant, f.after, f.size = t, after, size
	return []mailadmin.Message{{Key: "k1", Kind: "command", Template: "invoice_paid.v1", Recipient: "ada@example.com", State: "delivered", ClaimTime: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}}, "", f.err
}

func (f *fakeMail) PreviewPriceChange(_ context.Context, in mailadmin.PriceChangePreview) (mailadmin.Rendered, error) {
	f.preview = in
	return mailadmin.Rendered{Subject: "S", HTML: "<p>x</p>", Text: "x"}, f.err
}

// The mail views of a tenant are asked of the notifier for the tenant of the credential and nobody else; a deployment without the notifier's listener says so, and a
// notifier that fails does not leak its reason.
func TestMailViewsAskTheNotifierForTheCallersTenantOnly(t *testing.T) {
	fm := &fakeMail{}
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) { d.Mail = fm })

	resp, m := do(t, ts, "GET", "/v1/mail-suppressions?page_size=20&page_token=zed@example.com", "live-key", "")
	rows, _ := m["suppressions"].([]any)
	if resp.StatusCode != 200 || len(rows) != 1 || rows[0].(map[string]any)["address"] != "ada@example.com" || m["next_page_token"] != "ada@example.com" || fm.tenant != liveTenant || fm.size != 20 || fm.after != "zed@example.com" {
		t.Fatalf("suppressions: %d %v tenant %v size %d after %q", resp.StatusCode, m, fm.tenant, fm.size, fm.after)
	}
	if resp, m := do(t, ts, "GET", "/v1/mail-suppressions?page_size=9999", "live-key", ""); resp.StatusCode != 400 {
		t.Errorf("a page size of 9999: %d %v", resp.StatusCode, m)
	}
	fm.tenant = uuid.Nil
	if resp, _ := do(t, ts, "GET", "/v1/mail-messages", "live-key", ""); resp.StatusCode != 200 || fm.tenant != liveTenant || fm.size != 50 {
		t.Errorf("messages: %d tenant %v size %d", resp.StatusCode, fm.tenant, fm.size)
	}
	if resp, _ := do(t, ts, "DELETE", "/v1/mail-suppressions/ada@example.com", "live-key", ""); resp.StatusCode != 204 || fm.removed != "ada@example.com" || fm.tenant != liveTenant {
		t.Errorf("lift: %d %q", resp.StatusCode, fm.removed)
	}
	for _, bad := range []string{"not-an-address", "a@b.c%2Fd", "a@b.c%20d"} {
		if resp, _ := do(t, ts, "DELETE", "/v1/mail-suppressions/"+bad, "live-key", ""); resp.StatusCode != 400 {
			t.Errorf("lift %q: %d, want 400", bad, resp.StatusCode)
		}
	}
	fm.err = mailadmin.ErrNotFound
	if resp, m := do(t, ts, "DELETE", "/v1/mail-suppressions/gone@example.com", "live-key", ""); resp.StatusCode != 404 || errKey(m) != "gateway.mail.suppression_not_found" {
		t.Errorf("lifting what is not there: %d %v", resp.StatusCode, m)
	}
	fm.err = errors.New("dial tcp 10.0.0.7:9111: connection refused")
	resp, m = do(t, ts, "GET", "/v1/mail-messages", "live-key", "")
	if resp.StatusCode != 503 || errKey(m) != "gateway.mail.unavailable" || strings.Contains(resp.Header.Get("Content-Type")+m["error"].(map[string]any)["message"].(string), "10.0.0.7") {
		t.Errorf("a notifier that fails: %d %v", resp.StatusCode, m)
	}
	// Without a key there is no tenant, and a read-only key may read but not lift.
	if resp, _ := do(t, ts, "GET", "/v1/mail-messages", "", ""); resp.StatusCode != 401 {
		t.Errorf("no key: %d", resp.StatusCode)
	}
}

func TestMailPreviewValidatesWhatItIsGiven(t *testing.T) {
	fm := &fakeMail{}
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) { d.Mail = fm })
	body := `{"locale":"tr","customer_name":"Ada","plan_code":"pro","effective_time":"2026-12-05T10:00:00Z","new_renewal_minor":4000}`
	if resp, m := do(t, ts, "POST", "/v1/mail-previews/price-change", "live-key", body); resp.StatusCode != 200 || m["subject"] != "S" || m["html"] != "<p>x</p>" || fm.preview.Locale != "tr" || fm.preview.NewRenewalMinor != 4000 ||
		fm.preview.Timezone != "Europe/Istanbul" {
		t.Fatalf("preview: %d %v %+v", resp.StatusCode, m, fm.preview)
	}
	// The tenant's own zone comes from plans-svc, not from the request: a zone in the body is not read.
	if resp, _ := do(t, ts, "POST", "/v1/mail-previews/price-change", "live-key", `{"plan_code":"pro","effective_time":"2026-12-05T10:00:00Z","new_renewal_minor":1,"timezone":"UTC","currency":"JPY"}`); resp.StatusCode != http.StatusOK ||
		fm.preview.Timezone != "Europe/Istanbul" || fm.preview.Currency == "JPY" {
		t.Errorf("a zone and a currency in the body were read: %+v", fm.preview)
	}
	for name, bad := range map[string]string{"no date": `{"plan_code":"pro"}`, "no plan": `{"effective_time":"2026-12-05T10:00:00Z"}`, "negative": `{"plan_code":"pro","effective_time":"2026-12-05T10:00:00Z","new_renewal_minor":-1}`,
		"long name": `{"plan_code":"pro","effective_time":"2026-12-05T10:00:00Z","customer_name":"` + strings.Repeat("x", 201) + `"}`, "not JSON": `{`} {
		if resp, _ := do(t, ts, "POST", "/v1/mail-previews/price-change", "live-key", bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, resp.StatusCode)
		}
	}
}

// A deployment without the notifier's listener: the two views the console opens on every visit answer 200 with an empty list and "connected": false, so a page that is
// merely not connected logs nothing in the browser; the actions (lift, preview) still answer 503, and a connected one says "connected": true.
func TestMailViewsWithoutTheNotifierAreEmptyAndSayTheyAreNotConnected(t *testing.T) {
	bare, _ := newTestServer(t)
	for path, list := range map[string]string{"/v1/mail-suppressions": "suppressions", "/v1/mail-messages": "messages"} {
		resp, m := do(t, bare, "GET", path, "live-key", "")
		rows, isList := m[list].([]any)
		if resp.StatusCode != 200 || !isList || len(rows) != 0 || m["connected"] != false || m["next_page_token"] != "" {
			t.Errorf("GET %s without the notifier: %d %v, want 200 with an empty %s and connected false", path, resp.StatusCode, m, list)
		}
		if resp, _ := do(t, bare, "GET", path, "", ""); resp.StatusCode != 401 {
			t.Errorf("GET %s without the notifier and without a key: %d, want 401", path, resp.StatusCode)
		}
	}
	if resp, m := do(t, bare, "DELETE", "/v1/mail-suppressions/ada@example.com", "live-key", ""); resp.StatusCode != 503 || errKey(m) != "gateway.mail.unavailable" {
		t.Errorf("lifting without the notifier: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, bare, "POST", "/v1/mail-previews/price-change", "live-key", `{"plan_code":"pro","effective_time":"2026-12-05T10:00:00Z","new_renewal_minor":1}`); resp.StatusCode != 503 || errKey(m) != "gateway.mail.unavailable" {
		t.Errorf("previewing without the notifier: %d %v", resp.StatusCode, m)
	}
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) { d.Mail = &fakeMail{} })
	for _, path := range []string{"/v1/mail-suppressions", "/v1/mail-messages"} {
		if resp, m := do(t, ts, "GET", path, "live-key", ""); resp.StatusCode != 200 || m["connected"] != true {
			t.Errorf("GET %s with the notifier: %d %v, want connected true", path, resp.StatusCode, m)
		}
	}
}

const mailHookSecret = "whsec_" + "dGhlLXN2aXgtc2VjcmV0LW9mLXRoZS10ZXN0cw=="

// post sends a webhook the way the provider does: signed with Svix over the id, the timestamp and the raw body.
func postMailHook(t *testing.T, base, body, secret string, at time.Time) *http.Response {
	t.Helper()
	sig, err := mailx.SvixSign(secret, "msg_1", at, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/inbound/mail-events", bytes.NewReader([]byte(body)))
	req.Header.Set("svix-id", "msg_1")
	req.Header.Set("svix-timestamp", strconv.FormatInt(at.Unix(), 10))
	req.Header.Set("svix-signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

// The provider's delivery webhook is the way a bounce or a complaint reaches the suppression list: a request signed with the provider's secret becomes the notifier's
// neutral events, one for each recipient; one that is not signed, is signed with another secret or is old is refused and reaches nothing; an event that says nothing about
// delivery is accepted and ignored; a notifier that is down is a 503 so that the provider tries again; and the endpoint does not exist until a secret is set.
func TestMailDeliveryWebhookIsTranslatedForTheNotifier(t *testing.T) {
	fm := &fakeMail{}
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) { d.Mail, d.MailWebhookSecret = fm, mailHookSecret })
	now := time.Now()
	bounce := `{"type":"email.bounced","created_at":"2026-11-02T10:00:00Z","data":{"email_id":"em_1","to":["ada@example.com","bob@example.com"],"bounce":{"type":"Permanent","message":"no such user"}}}`

	if resp := postMailHook(t, ts.URL, bounce, mailHookSecret, now); resp.StatusCode != 200 || len(fm.events) != 2 || fm.events[0].Type != mailx.EventBounced || fm.events[0].BounceType != mailx.BounceHard ||
		fm.events[0].MessageID != "em_1" || fm.events[0].Recipient != "ada@example.com" || fm.events[1].Recipient != "bob@example.com" {
		t.Fatalf("a signed bounce: %d %+v", resp.StatusCode, fm.events)
	}
	fm.events = nil
	for name, c := range map[string]struct {
		secret string
		at     time.Time
	}{
		"another secret": {"whsec_" + "YW5vdGhlci1zZWNyZXQtb2YtdGhlLXRlc3Rz", now},
		"an old request": {mailHookSecret, now.Add(-10 * time.Minute)},
	} {
		if resp := postMailHook(t, ts.URL, bounce, c.secret, c.at); resp.StatusCode != 401 || len(fm.events) != 0 {
			t.Errorf("%s: %d %+v: nothing may reach the notifier", name, resp.StatusCode, fm.events)
		}
	}
	unsigned, _ := http.NewRequest(http.MethodPost, ts.URL+"/inbound/mail-events", strings.NewReader(bounce))
	if resp, err := http.DefaultClient.Do(unsigned); err != nil || resp.StatusCode != 401 || len(fm.events) != 0 {
		t.Errorf("a request with no signature: %v %v", resp, err)
	}
	if resp := postMailHook(t, ts.URL, `{"type":"email.opened","data":{"email_id":"em_1"}}`, mailHookSecret, now); resp.StatusCode != 200 || len(fm.events) != 0 {
		t.Errorf("an event that says nothing about delivery: %d %+v", resp.StatusCode, fm.events)
	}
	if resp := postMailHook(t, ts.URL, `not json`, mailHookSecret, now); resp.StatusCode != 400 {
		t.Errorf("a signed body that is not an event: %d", resp.StatusCode)
	}

	fm.eventErr = mailadmin.BadRequestError{Message: "unknown event"}
	if resp := postMailHook(t, ts.URL, bounce, mailHookSecret, now); resp.StatusCode != 200 {
		t.Errorf("an event the notifier refuses for good is not retried: %d", resp.StatusCode)
	}
	fm.eventErr = errors.New("dial tcp 10.0.0.7:9111: connection refused")
	if resp := postMailHook(t, ts.URL, bounce, mailHookSecret, now); resp.StatusCode != 503 {
		t.Errorf("a notifier that is down: %d, want 503 so that the provider tries again", resp.StatusCode)
	}

	off, _, _, _ := newTestServerWith(t, func(d *Deps) { d.Mail = fm })
	if resp := postMailHook(t, off.URL, bounce, mailHookSecret, now); resp.StatusCode != 404 {
		t.Errorf("without a secret the endpoint does not exist: %d", resp.StatusCode)
	}
	noNotifier, _, _, _ := newTestServerWith(t, func(d *Deps) { d.MailWebhookSecret = mailHookSecret })
	if resp := postMailHook(t, noNotifier.URL, bounce, mailHookSecret, now); resp.StatusCode != 404 {
		t.Errorf("without a notifier there is nowhere to hand an event: %d", resp.StatusCode)
	}
}
