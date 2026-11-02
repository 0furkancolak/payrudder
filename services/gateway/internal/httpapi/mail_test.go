package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/gateway/internal/mailadmin"
)

// fakeMail records what the gateway asks of the notifier and answers from fixtures.
type fakeMail struct {
	tenant  uuid.UUID
	after   string
	size    int
	removed string
	preview mailadmin.PriceChangePreview
	err     error
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
	// A deployment without the notifier's listener.
	bare, _ := newTestServer(t)
	if resp, m := do(t, bare, "GET", "/v1/mail-suppressions", "live-key", ""); resp.StatusCode != 503 || errKey(m) != "gateway.mail.unavailable" {
		t.Errorf("without the notifier: %d %v", resp.StatusCode, m)
	}
}
