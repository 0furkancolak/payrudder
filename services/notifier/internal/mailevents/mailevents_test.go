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

var tenantA = uuid.MustParse("0193c000-0000-7000-8000-00000000000a")

// memory is the notes and the suppression list in memory.
type memory struct {
	mu   sync.Mutex
	sent map[string]struct {
		tenant    uuid.UUID
		recipient string
	}
	rows map[string]store.Suppression // tenant/address
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
		sig = webhooksig.Sign(time.Now(), body, secret)
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
	for name, sig := range map[string]string{
		"wrong secret": webhooksig.Sign(time.Now(), body, "other"),
		"stale":        webhooksig.Sign(time.Now().Add(-time.Hour), body, secret),
		"none":         "garbage",
	} {
		if code, _ := do(t, "POST", srv.URL+"/v1/mail-events", body, sig); code != 401 {
			t.Errorf("%s: %d, want 401", name, code)
		}
	}
	if code, _ := do(t, "GET", srv.URL+"/v1/mail-suppressions?tenant_id="+tenantA.String(), nil, webhooksig.Sign(time.Now(), nil, "other")); code != 401 {
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
	req.Header.Set(webhooksig.Header(), webhooksig.Sign(time.Now(), body, ""))
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
