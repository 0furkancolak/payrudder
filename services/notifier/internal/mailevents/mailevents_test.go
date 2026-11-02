package mailevents

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/webhooksig"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
)

const secret = "whsec_test"

var (
	tenantA = uuid.MustParse("0193c000-0000-7000-8000-00000000000a")
	tenantB = uuid.MustParse("0193c000-0000-7000-8000-00000000000b")
)

// memory is the notes and the suppression list in memory.
type memory struct {
	mu   sync.Mutex
	sent map[string]struct {
		tenant    uuid.UUID
		recipient string
	}
	rows   map[string]store.Suppression // tenant/address
	states map[string]string            // message id: what the provider reported
}

func newMemory() *memory {
	m := &memory{rows: map[string]store.Suppression{}}
	m.sent = map[string]struct {
		tenant    uuid.UUID
		recipient string
	}{
		"fake-1": {tenantA, "ada@example.com"}, "fake-2": {tenantA, "bob@example.com"}, "fake-3": {tenantA, "cyd@example.com"},
	}
	return m
}

func (m *memory) ByMessageID(_ context.Context, id string) (uuid.UUID, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sent[id]
	return s.tenant, s.recipient, ok, nil
}

func (m *memory) RecordDelivery(_ context.Context, id, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.states == nil {
		m.states = map[string]string{}
	}
	if cur := m.states[id]; cur != "bounced" && cur != "complained" {
		m.states[id] = state
	}
	return nil
}

func key(t uuid.UUID, a string) string { return t.String() + "/" + strings.ToLower(a) }

func (m *memory) Add(_ context.Context, s store.Suppression) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(s.TenantID, s.Address)
	if _, ok := m.rows[k]; ok {
		return false, nil
	}
	s.Address = strings.ToLower(s.Address)
	m.rows[k] = s
	return true, nil
}

func (m *memory) List(_ context.Context, t uuid.UUID, _ string, _ int32) ([]store.Suppression, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Suppression
	for _, s := range m.rows {
		if s.TenantID == t {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memory) Remove(_ context.Context, t uuid.UUID, a string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(t, a)
	_, ok := m.rows[k]
	delete(m.rows, k)
	return ok, nil
}

func serve(t *testing.T) (*httptest.Server, *memory) {
	t.Helper()
	mem := newMemory()
	h := &Handler{Secret: secret, Notes: mem, List: mem, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv, mem
}

func do(t *testing.T, method, url string, body []byte, sig string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(string(body)))
	if sig == "" {
		sig = webhooksig.SignRequest(time.Now(), method, req.URL, body, secret)
	}
	req.Header.Set(webhooksig.Header(), sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHardBounceAndComplaintSuppressTheRecordedAddress(t *testing.T) {
	srv, mem := serve(t)
	// The address comes from the notifier's own record of the mail (the event says "Ada@Example.com", the record "ada@example.com").
	code, m := do(t, "POST", srv.URL+"/v1/mail-events", fixture(t, "hard-bounce.json"), "")
	if code != 200 || m["matched"] != true || m["suppressed"] != true {
		t.Fatalf("hard bounce: %d %v", code, m)
	}
	if s := mem.rows[key(tenantA, "ada@example.com")]; s.Reason != store.ReasonBounce || s.Source != "fake-1" || !strings.Contains(s.Detail, "does not exist") {
		t.Errorf("row = %+v", s)
	}
	// The same event again is idempotent: the address is there, nothing changes.
	if code, m = do(t, "POST", srv.URL+"/v1/mail-events", fixture(t, "hard-bounce.json"), ""); code != 200 || m["suppressed"] != false || len(mem.rows) != 1 {
		t.Errorf("repeat: %d %v rows=%d", code, m, len(mem.rows))
	}
	if code, m = do(t, "POST", srv.URL+"/v1/mail-events", fixture(t, "complaint.json"), ""); code != 200 || m["suppressed"] != true || mem.rows[key(tenantA, "cyd@example.com")].Reason != store.ReasonComplaint {
		t.Errorf("complaint: %d %v", code, m)
	}
}

func TestOtherEventsAreOnlyCounted(t *testing.T) {
	srv, mem := serve(t)
	for _, f := range []string{"soft-bounce.json", "delivered.json"} {
		if code, m := do(t, "POST", srv.URL+"/v1/mail-events", fixture(t, f), ""); code != 200 || m["matched"] != true || m["suppressed"] != false {
			t.Errorf("%s: %d %v", f, code, m)
		}
	}
	if len(mem.rows) != 0 {
		t.Errorf("a soft bounce and a delivery must not suppress anyone: %v", mem.rows)
	}
}

// What the provider reports is kept for the message log, and a permanent bounce is the last word: a late delivery notice does not take it back.
func TestEventsAreKeptForTheMessageLog(t *testing.T) {
	srv, mem := serve(t)
	post := func(f string) { do(t, "POST", srv.URL+"/v1/mail-events", fixture(t, f), "") }
	post("delivered.json")   // fake-1
	post("soft-bounce.json") // fake-2
	if mem.states["fake-1"] != "delivered" || mem.states["fake-2"] != "soft_bounced" {
		t.Fatalf("states = %v", mem.states)
	}
	post("hard-bounce.json") // fake-1 again: now it bounced for good
	if mem.states["fake-1"] != "bounced" {
		t.Errorf("a permanent bounce is kept as bounced: %v", mem.states)
	}
	post("delivered.json") // a late notice for fake-1
	if mem.states["fake-1"] != "bounced" {
		t.Errorf("a late delivery took back a bounce: %v", mem.states)
	}
	post("complaint.json") // fake-3
	if mem.states["fake-3"] != "complained" {
		t.Errorf("a complaint is kept: %v", mem.states)
	}
}

func TestUnmatchedAndForeignRecipientEventsSuppressNothing(t *testing.T) {
	srv, mem := serve(t)
	for _, f := range []string{"unknown-message.json", "other-address.json"} {
		if code, m := do(t, "POST", srv.URL+"/v1/mail-events", fixture(t, f), ""); code != 200 || m["matched"] != false {
			t.Errorf("%s: %d %v", f, code, m)
		}
	}
	if len(mem.rows) != 0 {
		t.Errorf("rows = %v", mem.rows)
	}
}

func TestBadEventsAndBadSignatures(t *testing.T) {
	srv, mem := serve(t)
	if code, _ := do(t, "POST", srv.URL+"/v1/mail-events", fixture(t, "bounce-without-type.json"), ""); code != 400 {
		t.Errorf("a bounce that does not say whether it is permanent: %d", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/v1/mail-events", []byte("not json"), ""); code != 400 {
		t.Errorf("not json: %d", code)
	}
	body := fixture(t, "hard-bounce.json")
	events, _ := http.NewRequest("POST", srv.URL+"/v1/mail-events", nil)
	for name, sig := range map[string]string{
		"wrong secret": webhooksig.SignRequest(time.Now(), "POST", events.URL, body, "other"),
		"stale":        webhooksig.SignRequest(time.Now().Add(-time.Hour), "POST", events.URL, body, secret),
		"v1 only":      webhooksig.Sign(time.Now(), body, secret),
		"none":         "garbage",
	} {
		if code, _ := do(t, "POST", srv.URL+"/v1/mail-events", body, sig); code != 401 {
			t.Errorf("%s: %d, want 401", name, code)
		}
	}
	list, _ := http.NewRequest("GET", srv.URL+"/v1/mail-suppressions?tenant_id="+tenantA.String(), nil)
	if code, _ := do(t, "GET", list.URL.String(), nil, webhooksig.SignRequest(time.Now(), "GET", list.URL, nil, "other")); code != 401 {
		t.Errorf("a list with the wrong secret: %d", code)
	}
	if len(mem.rows) != 0 {
		t.Errorf("a refused request changed the list: %v", mem.rows)
	}
	// Without a secret configured, nothing is accepted (not even a correctly "signed" request with an empty secret).
	h := &Handler{Notes: mem, List: mem, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	noSecret := httptest.NewServer(h.Routes())
	defer noSecret.Close()
	req, _ := http.NewRequest("POST", noSecret.URL+"/v1/mail-events", strings.NewReader(string(body)))
	req.Header.Set(webhooksig.Header(), webhooksig.SignRequest(time.Now(), "POST", req.URL, body, ""))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Errorf("no secret configured: %v %v", resp, err)
	}
}

func TestListAddAndLiftASuppression(t *testing.T) {
	srv, mem := serve(t)
	base := srv.URL + "/v1/mail-suppressions"
	addr := url.PathEscape("Dee@Example.com")
	if code, m := do(t, "PUT", base+"/"+tenantA.String()+"/"+addr, []byte(`{"detail":"asked not to be mailed"}`), ""); code != 200 || m["added"] != true {
		t.Fatalf("add: %d %v", code, m)
	}
	if mem.rows[key(tenantA, "dee@example.com")].Reason != store.ReasonManual {
		t.Errorf("rows = %v", mem.rows)
	}
	code, m := do(t, "GET", base+"?tenant_id="+tenantA.String(), nil, "")
	if rows, _ := m["suppressions"].([]any); code != 200 || len(rows) != 1 || rows[0].(map[string]any)["address"] != "dee@example.com" {
		t.Errorf("list: %d %v", code, m)
	}
	if code, _ = do(t, "GET", base+"?tenant_id=nope", nil, ""); code != 400 {
		t.Errorf("list without a tenant: %d", code)
	}
	if code, _ = do(t, "DELETE", base+"/"+tenantA.String()+"/dee@example.com", nil, ""); code != 200 || len(mem.rows) != 0 {
		t.Errorf("lift: %d rows=%v", code, mem.rows)
	}
	if code, _ = do(t, "DELETE", base+"/"+tenantA.String()+"/dee@example.com", nil, ""); code != 404 {
		t.Errorf("lifting what is not there: %d", code)
	}
	if code, _ = do(t, "PUT", base+"/"+tenantA.String()+"/not-an-address", nil, ""); code != 400 {
		t.Errorf("add a non-address: %d", code)
	}
}

// messageLog is a tenant's mail in memory, newest first, paged like the store pages it.
type messageLog struct{ rows []store.Message }

func (l messageLog) ListForTenant(_ context.Context, t uuid.UUID, after store.MessagesAfter, size int32) ([]store.Message, error) {
	if t != tenantA {
		return nil, nil
	}
	var out []store.Message
	for _, m := range l.rows {
		if after.Key == "" || m.ClaimTime.Before(after.At) || (m.ClaimTime.Equal(after.At) && m.Key < after.Key) {
			out = append(out, m)
		}
	}
	return out[:min(len(out), int(size))], nil
}

func serveMessages(t *testing.T, log Messages, preview func(PriceChangePreview) (Rendered, error)) *httptest.Server {
	t.Helper()
	mem := newMemory()
	h := &Handler{Secret: secret, Notes: mem, List: mem, Messages: log, PreviewPriceChange: preview, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv
}

// The message log answers a tenant's mail newest first, a page at a time; the token it gives gets the next page and nothing is repeated or missed; another tenant sees
// none of it; a token that is not its own is refused.
func TestMessageLogPagesNewestFirst(t *testing.T) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	var rows []store.Message
	for i := range 5 {
		rows = append(rows, store.Message{Key: "k" + string(rune('5'-i)), Kind: "command", Template: "invoice_paid.v1", Recipient: "ada@example.com", State: "sent", ClaimTime: at.Add(-time.Duration(i) * time.Minute)})
	}
	rows[1].State, rows[2].State = "bounced", "sending"
	srv := serveMessages(t, messageLog{rows}, nil)
	url := srv.URL + "/v1/mail-messages?tenant_id=" + tenantA.String() + "&page_size=2"
	var keys []string
	next := ""
	for range 5 {
		code, m := do(t, "GET", url+"&after="+next, nil, "")
		if code != 200 {
			t.Fatalf("page: %d %v", code, m)
		}
		for _, r := range m["messages"].([]any) {
			keys = append(keys, r.(map[string]any)["key"].(string))
		}
		if next, _ = m["next_after"].(string); next == "" {
			break
		}
	}
	if strings.Join(keys, ",") != "k5,k4,k3,k2,k1" {
		t.Fatalf("messages in pages of two = %v", keys)
	}
	if _, m := do(t, "GET", srv.URL+"/v1/mail-messages?tenant_id="+uuid.NewString(), nil, ""); len(m["messages"].([]any)) != 0 {
		t.Errorf("another tenant sees %v", m["messages"])
	}
	for _, bad := range []string{"&after=garbage", "&page_size=0", "&page_size=501"} {
		if code, _ := do(t, "GET", srv.URL+"/v1/mail-messages?tenant_id="+tenantA.String()+bad, nil, ""); code != 400 {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}
	if code, _ := do(t, "GET", srv.URL+"/v1/mail-messages", nil, ""); code != 400 {
		t.Errorf("no tenant: %d", code)
	}
	if code, _ := do(t, "GET", url, nil, "t=1,v1=bad"); code != 401 {
		t.Errorf("a bad signature: %d", code)
	}
}

// The preview renders what it is given and says why it cannot; both routes answer 501 where the notifier was started without them.
func TestPreviewAnswersTheRenderedMail(t *testing.T) {
	var got PriceChangePreview
	srv := serveMessages(t, nil, func(in PriceChangePreview) (Rendered, error) {
		got = in
		if in.Locale == "xx" {
			return Rendered{}, io.ErrUnexpectedEOF
		}
		return Rendered{Subject: "S " + in.PlanCode, HTML: "<p>H</p>", Text: "T"}, nil
	})
	body := `{"locale":"de","customer_name":"Ada","tenant_name":"Acme","plan_code":"pro","effective_time":"2026-12-05T10:00:00Z","new_renewal_minor":4000,"currency":"EUR","timezone":"Europe/Berlin"}`
	code, m := do(t, "POST", srv.URL+"/v1/mail-previews/price-change", []byte(body), "")
	if code != 200 || m["subject"] != "S pro" || m["html"] != "<p>H</p>" || got.NewRenewalMinor != 4000 || got.Locale != "de" || got.EffectiveTime.Year() != 2026 {
		t.Fatalf("preview: %d %v %+v", code, m, got)
	}
	if code, _ := do(t, "POST", srv.URL+"/v1/mail-previews/price-change", []byte(`{"plan_code":"pro"}`), ""); code != 400 {
		t.Errorf("a preview without a date and a currency: %d", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/v1/mail-previews/price-change", []byte(strings.Replace(body, `"de"`, `"xx"`, 1)), ""); code != 400 {
		t.Errorf("a preview that cannot be rendered: %d", code)
	}
	bare := serveMessages(t, nil, nil)
	if code, _ := do(t, "POST", bare.URL+"/v1/mail-previews/price-change", []byte(body), ""); code != 501 {
		t.Errorf("preview without a renderer: %d", code)
	}
	if code, _ := do(t, "GET", bare.URL+"/v1/mail-messages?tenant_id="+tenantA.String(), nil, ""); code != 501 {
		t.Errorf("messages without a log: %d", code)
	}
}

// The v1 signature of an empty body covers only the time, so a captured GET of the list was a valid DELETE of a suppression (and the reverse) for five minutes. A v2
// signature covers the method, the path and the query: the same header sent to another route is refused.
func TestASignatureOfOneRequestIsNoGoodForAnother(t *testing.T) {
	srv, mem := serve(t)
	base := srv.URL + "/v1/mail-suppressions"
	if code, _ := do(t, "PUT", base+"/"+tenantA.String()+"/ada@example.com", nil, ""); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	listURL, _ := url.Parse(base + "?tenant_id=" + tenantA.String())
	captured := webhooksig.SignRequest(time.Now(), "GET", listURL, nil, secret)
	if code, _ := do(t, "GET", listURL.String(), nil, captured); code != 200 {
		t.Fatalf("the request it was signed for: %d", code)
	}
	for name, c := range map[string]struct{ method, url string }{
		"lifting a suppression": {"DELETE", base + "/" + tenantA.String() + "/ada@example.com"},
		"the other tenant":      {"GET", base + "?tenant_id=" + tenantB.String()},
		"the message log":       {"GET", srv.URL + "/v1/mail-messages?tenant_id=" + tenantA.String()},
	} {
		if code, _ := do(t, c.method, c.url, nil, captured); code != 401 {
			t.Errorf("%s with a captured signature: %d, want 401", name, code)
		}
	}
	if len(mem.rows) != 1 {
		t.Errorf("a replayed signature changed the list: %v", mem.rows)
	}
	// The same through v1: the signature of an empty body, which the previous release accepted anywhere.
	v1 := webhooksig.Sign(time.Now(), nil, secret)
	if code, _ := do(t, "DELETE", base+"/"+tenantA.String()+"/ada@example.com", nil, v1); code != 401 || len(mem.rows) != 1 {
		t.Errorf("a v1 signature of an empty body lifting a suppression: %d, rows %v", code, mem.rows)
	}
}

// During a rollout from a gateway that still signs v1, the notifier can be told to accept it (AcceptV1); it is off by default.
func TestVersionOneSignaturesAreAcceptedOnlyWhenAsked(t *testing.T) {
	mem := newMemory()
	h := &Handler{Secret: secret, Notes: mem, List: mem, AcceptV1: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()
	listURL := srv.URL + "/v1/mail-suppressions?tenant_id=" + tenantA.String()
	if code, _ := do(t, "GET", listURL, nil, webhooksig.Sign(time.Now(), nil, secret)); code != 200 {
		t.Errorf("v1 during a rollout: %d", code)
	}
	if code, _ := do(t, "GET", listURL, nil, webhooksig.Sign(time.Now(), nil, "other")); code != 401 {
		t.Errorf("v1 with another secret: %d", code)
	}
	if code, _ := do(t, "GET", listURL, nil, ""); code != 200 {
		t.Errorf("v2 still works: %d", code)
	}
	off, _ := serve(t)
	if code, _ := do(t, "GET", off.URL+"/v1/mail-suppressions?tenant_id="+tenantA.String(), nil, webhooksig.Sign(time.Now(), nil, secret)); code != 401 {
		t.Errorf("v1 by default: %d, want 401", code)
	}
}
