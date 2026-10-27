package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// seenRequest is what the fake payments-worker received.
type seenRequest struct {
	method, path string
	query        url.Values
	actor, token string
	body         string
}

// fakePayments is a payments-worker that records what it is sent and answers the go-live read with the given accounts.
type fakePayments struct {
	mu       sync.Mutex
	seen     []seenRequest
	accounts []map[string]any
	status   int // of the provider-account writes; 0 is 200
	srv      *httptest.Server
}

func newFakePayments(t *testing.T) *fakePayments {
	f := &fakePayments{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.seen = append(f.seen, seenRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query(), actor: r.Header.Get("X-Payrudder-Actor"),
			token: r.Header.Get("Authorization"), body: string(b)})
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && (strings.HasSuffix(r.URL.Path, "/go-live") || strings.HasSuffix(r.URL.Path, "/provider-health")):
			_ = json.NewEncoder(w).Encode(map[string]any{"ready": len(f.accounts) > 0, "providers": []string{}, "accounts": f.accounts})
		case f.status != 0:
			w.WriteHeader(f.status)
			_, _ = io.WriteString(w, `{"error":{"code":"INVALID_ARGUMENT","key":"payments.account.health_check_failed","message":"the provider did not accept these credentials"}}`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = io.WriteString(w, `{"provider":"iyzico","environment":"live","default":true}`)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePayments) deps() func(*Deps) {
	return func(d *Deps) {
		u, _ := url.Parse(f.srv.URL)
		d.PaymentsURL = u
		d.PaymentsToken = "internal-token"
	}
}

func (f *fakePayments) writes() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []seenRequest
	for _, s := range f.seen {
		if s.method != http.MethodGet {
			out = append(out, s)
		}
	}
	return out
}

// The profile's owner and app come from the session and the path, whatever the body says; a member who is not the owner and another account's app are refused.
func TestBillingProfileRoute(t *testing.T) {
	p := newProduct(t, nil, nil)
	body := `{"legal":{"legal_name":"Ada Labs","tax_id":"DE123456789"},"address":{"line1":"Hauptstr. 1","city":"Berlin","country":"DE"},"user_id":"` + bobUser + `","app_id":"` + bobApp + `"}`
	resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/billing-profile", tokAda, "", body)
	if resp.StatusCode != 200 || m["id"] != adaLive {
		t.Fatalf("save: %d %v", resp.StatusCode, m)
	}
	got := p.ident.profiles[0]
	if got.GetUserId() != adaUser || got.GetAppId() != adaApp || got.GetLegal().GetLegalName() != "Ada Labs" || got.GetAddress().GetCountry() != "DE" {
		t.Errorf("request = %v: the user comes from the session and the app from the path", got)
	}
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/billing-profile", tokViewer, "", body); resp.StatusCode != 403 || errKeyOf(m) != "identity.go_live.owner_only" {
		t.Errorf("a viewer: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+bobApp+"/billing-profile", tokAda, "", body); resp.StatusCode != 404 || errKeyOf(m) != "identity.app.not_found" {
		t.Errorf("another account's app: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/billing-profile", "", "", body); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
	if len(p.ident.profiles) != 1 {
		t.Errorf("%d profiles reached plans-svc, want the owner's one", len(p.ident.profiles))
	}

	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/go-live/terms", tokAda, "", ``); resp.StatusCode != 200 {
		t.Errorf("terms: %d", resp.StatusCode)
	}
	if resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/go-live/terms", tokViewer, "", ``); resp.StatusCode != 403 || errKeyOf(m) != "identity.go_live.owner_only" {
		t.Errorf("terms by a viewer: %d %v", resp.StatusCode, m)
	}
	if len(p.ident.terms) != 1 || p.ident.terms[0].GetUserId() != adaUser {
		t.Errorf("terms = %v", p.ident.terms)
	}
}

// A person's live provider account: the tenant and environment are the app's live ones, whatever the request says; the provider is asked to vouch for the
// credentials; the audit names the person; the first account becomes the default; and only the owner may change anything.
func TestLiveProviderRoutes(t *testing.T) {
	pw := newFakePayments(t)
	p := newProduct(t, nil, pw.deps())
	settings := `{"api_key":"api","secret_key":"SECRET-ONE","buyer":{"name":"Ada"}}`

	resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/go-live/providers/iyzico?environment=sandbox&verify=false&default=false&tenant=x", tokAda, "", settings)
	if resp.StatusCode != 200 || m["provider"] != "iyzico" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("connect: %d %v", resp.StatusCode, m)
	}
	w := pw.writes()
	if len(w) != 1 {
		t.Fatalf("payments-worker was written %d times", len(w))
	}
	got := w[0]
	if got.method != "PUT" || got.path != "/admin/tenants/"+adaLive+"/provider-accounts/iyzico" {
		t.Errorf("forwarded to %s %s: the tenant is the app's live one", got.method, got.path)
	}
	if got.query.Get("environment") != "live" || got.query.Get("verify") != "true" || got.query.Get("default") != "true" || len(got.query) != 3 {
		t.Errorf("query = %v: live, verified, the first account is the default, and nothing the caller added", got.query)
	}
	if got.actor != "user:"+adaUser || got.body != settings || got.token == "Bearer "+tokAda {
		t.Errorf("actor %q body %q token %q", got.actor, got.body, got.token)
	}

	// The browser cannot claim to be someone else.
	req, _ := http.NewRequest("PUT", p.ts.URL+"/app/v1/apps/"+adaApp+"/go-live/providers/paddle", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tokAda)
	req.Header.Set("X-Payrudder-Actor", "operator")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("paddle: %v %v", r, err)
	}
	if w := pw.writes(); w[1].actor != "user:"+adaUser {
		t.Errorf("actor = %q: a header of the browser's decided who the change is recorded for", w[1].actor)
	}

	// With a default account already, a second one does not take the place.
	pw.mu.Lock()
	pw.accounts = []map[string]any{{"provider": "iyzico", "default": true, "working": true}}
	pw.mu.Unlock()
	p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/go-live/providers/paddle", tokAda, "", `{}`)
	if w := pw.writes(); w[2].query.Get("default") != "false" {
		t.Errorf("default = %q: a second account must not take the default from the first", w[2].query.Get("default"))
	}

	// Not the owner, not the app's member, not a provider Payrudder has an adapter for: nothing is forwarded.
	before := len(pw.writes())
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/go-live/providers/iyzico", tokViewer, "", settings); resp.StatusCode != 403 || errKeyOf(m) != "identity.go_live.owner_only" {
		t.Errorf("viewer: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+bobApp+"/go-live/providers/iyzico", tokAda, "", settings); resp.StatusCode != 404 || errKeyOf(m) != "identity.app.not_found" {
		t.Errorf("another account's app: %d %v", resp.StatusCode, m)
	}
	for _, name := range []string{"mockpsp", "mock-psp", "stripe", "..%2Fx"} {
		if resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/go-live/providers/"+name, tokAda, "", settings); resp.StatusCode != 404 || errKeyOf(m) != "payments.provider.unknown" {
			t.Errorf("provider %q: %d %v", name, resp.StatusCode, m)
		}
	}
	if resp, _ := p.call(t, "DELETE", "/app/v1/apps/"+adaApp+"/go-live/providers/iyzico", tokViewer, "", ``); resp.StatusCode != 403 {
		t.Errorf("viewer delete: %d", resp.StatusCode)
	}
	if after := len(pw.writes()); after != before {
		t.Errorf("%d requests reached payments-worker for people who may not connect a provider", after-before)
	}

	// Removing.
	if resp, _ := p.call(t, "DELETE", "/app/v1/apps/"+adaApp+"/go-live/providers/iyzico?environment=sandbox", tokAda, "", ``); resp.StatusCode != 204 {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	if last := pw.writes()[len(pw.writes())-1]; last.method != "DELETE" || last.query.Get("environment") != "live" || last.actor != "user:"+adaUser || last.path != "/admin/tenants/"+adaLive+"/provider-accounts/iyzico" {
		t.Errorf("delete forwarded as %+v", last)
	}

	// The provider's refusal reaches the person with its key.
	pw.mu.Lock()
	pw.status = 400
	pw.mu.Unlock()
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/go-live/providers/iyzico", tokAda, "", settings); resp.StatusCode != 400 || errKeyOf(m) != "payments.account.health_check_failed" || strings.Contains(m2s(m), "SECRET-ONE") {
		t.Errorf("refused credentials: %d %v", resp.StatusCode, m)
	}
}

func m2s(m map[string]any) string { b, _ := json.Marshal(m); return string(b) }

// Any member reads the providers; a stranger does not; without payments-worker the answer is unavailable, never an empty list that reads as "none connected".
func TestLiveProvidersAreReadByMembers(t *testing.T) {
	pw := newFakePayments(t)
	pw.accounts = []map[string]any{{"provider": "paddle", "default": true, "working": false, "checked": true, "problem": "refused"}}
	p := newProduct(t, nil, pw.deps())
	for _, tok := range []string{tokAda, tokViewer} {
		resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/go-live/providers", tok, "", "")
		accounts, _ := m["accounts"].([]any)
		if resp.StatusCode != 200 || len(accounts) != 1 || resp.Header.Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s: %d %v", tok, resp.StatusCode, m)
		}
	}
	if resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/go-live/providers", tokBob, "", ""); resp.StatusCode != 404 || errKeyOf(m) != "identity.app.not_found" {
		t.Errorf("a stranger: %d %v", resp.StatusCode, m)
	}
	down := newProduct(t, nil, nil)
	if resp, m := down.call(t, "GET", "/app/v1/apps/"+adaApp+"/go-live/providers", tokAda, "", ""); resp.StatusCode != 503 || errKeyOf(m) != "gateway.payments.unreachable" {
		t.Errorf("without payments-worker: %d %v", resp.StatusCode, m)
	}
}
