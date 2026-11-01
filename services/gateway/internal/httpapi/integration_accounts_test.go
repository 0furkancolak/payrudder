package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type workerCall struct{ method, path, query, actor, body string }

// several e-invoice accounts of an app: the list and the default, a further account at one integrator, removing one by its key, moving the default, the routing
// by country and the audit trail. Everything goes to the live tenant of the app, only the owner changes anything, and a request that is not allowed or not
// well-formed never reaches payments-worker.
func TestEInvoiceAccountsOfAnApp(t *testing.T) {
	var mu sync.Mutex
	var seen []workerCall
	pw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, workerCall{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Payrudder-Actor"), string(b)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/provider-account-events"):
			_, _ = io.WriteString(w, `{"events":[{"id":9,"kind":"einvoice","account":"nilvera~tr","environment":"live","action":"default_set","actor":"user:ada","is_default":true,"verified_remote":false,"created_at":"2026-11-01T10:00:00Z"}],"next_before":9}`)
		case strings.HasSuffix(r.URL.Path, "/routing"):
			_, _ = io.WriteString(w, `{"rules":[{"country":"DE","account":"nilvera~tr"}]}`)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"accounts":[{"environment":"sandbox","account":"mock","provider":"mock","label":"","mode":"test","is_default":true},`+
				`{"environment":"live","account":"nilvera","provider":"nilvera","label":"","mode":"production","is_default":true,"updated_at":"2026-10-01T00:00:00Z"},`+
				`{"environment":"live","account":"nilvera~tr","provider":"nilvera","label":"tr","mode":"test","is_default":false,"updated_at":"2026-10-02T00:00:00Z"}]}`)
		case r.Method == http.MethodDelete || strings.HasSuffix(r.URL.Path, "/default"):
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer pw.Close()
	u, _ := url.Parse(pw.URL)
	p := newProduct(t, nil, func(d *Deps) { d.PaymentsURL = u; d.PaymentsToken = "internal-token" })
	base := "/app/v1/apps/" + adaApp + "/integrations/e-invoice"
	last := func() workerCall { mu.Lock(); defer mu.Unlock(); return seen[len(seen)-1] }
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(seen) }

	// The list holds the live accounts only, with the default among them as `account`.
	resp, m := p.call(t, "GET", base, tokViewer, "", "")
	accounts, _ := m["accounts"].([]any)
	def, _ := m["account"].(map[string]any)
	if resp.StatusCode != 200 || len(accounts) != 2 || def["account"] != "nilvera" || def["is_default"] != true || strings.Contains(toJSON(m), "mock") {
		t.Fatalf("list: %d %v", resp.StatusCode, m)
	}
	if second, _ := accounts[1].(map[string]any); second["account"] != "nilvera~tr" || second["label"] != "tr" || second["provider"] != "nilvera" || second["is_default"] != false {
		t.Errorf("second account: %v", second)
	}

	// A further account at the same integrator: the body goes as typed, for the live tenant, as this person.
	put := `{"provider":"nilvera","label":"tr","default":true,"mode":"test","credentials":{"api_key":"NV-SECRET"}}`
	if resp, _ := p.call(t, "PUT", base, tokAda, "", put); resp.StatusCode != 200 {
		t.Fatalf("put: %d", resp.StatusCode)
	}
	if w := last(); w.method != "PUT" || w.path != "/admin/tenants/"+adaLive+"/e-invoice-accounts/live" || w.body != put || w.actor != "user:"+adaUser {
		t.Errorf("put forwarded %+v", w)
	}

	// Removing, testing and defaulting an account name it by key; only that key reaches payments-worker as a query, whatever else was asked.
	if resp, _ := p.call(t, "DELETE", base+"?account=nilvera~tr&environment=sandbox", tokAda, "", ""); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if w := last(); w.path != "/admin/tenants/"+adaLive+"/e-invoice-accounts/live" || w.query != "account=nilvera~tr" || w.actor != "user:"+adaUser {
		t.Errorf("delete forwarded %+v", w)
	}
	if resp, _ := p.call(t, "POST", base+"/default", tokAda, "", `{"account":"nilvera~tr"}`); resp.StatusCode != 204 {
		t.Fatalf("default: %d", resp.StatusCode)
	}
	if w := last(); w.method != "POST" || w.path != "/admin/tenants/"+adaLive+"/e-invoice-accounts/live/default" || w.query != "account=nilvera~tr" || w.actor != "user:"+adaUser || w.body != "" {
		t.Errorf("default forwarded %+v: the account in the query, no body", w)
	}
	if resp, _ := p.call(t, "POST", base+"/test", tokAda, "", `{"account":"nilvera~tr"}`); resp.StatusCode != 200 || last().body != `{"account":"nilvera~tr"}` {
		t.Errorf("test: %d %+v", resp.StatusCode, last())
	}

	// Routing: any member reads, the owner replaces.
	if resp, m := p.call(t, "GET", base+"/routing", tokViewer, "", ""); resp.StatusCode != 200 || !strings.Contains(toJSON(m), "nilvera~tr") || last().path != "/admin/tenants/"+adaLive+"/e-invoice-accounts/live/routing" {
		t.Errorf("routing get: %d %v", resp.StatusCode, m)
	}
	rules := `{"rules":[{"country":"DE","account":"nilvera~tr"}]}`
	if resp, _ := p.call(t, "PUT", base+"/routing", tokAda, "", rules); resp.StatusCode != 200 || last().method != "PUT" || last().body != rules || last().actor != "user:"+adaUser {
		t.Errorf("routing put: %d %+v", resp.StatusCode, last())
	}

	// A viewer changes nothing and a malformed key goes nowhere: payments-worker is not bothered.
	before := count()
	for name, c := range map[string]struct{ method, path, token, body string }{
		"a viewer's delete":      {"DELETE", base + "?account=nilvera", tokViewer, ""},
		"a viewer's default":     {"POST", base + "/default", tokViewer, `{"account":"nilvera"}`},
		"a viewer's routing":     {"PUT", base + "/routing", tokViewer, rules},
		"a key with two labels":  {"DELETE", base + "?account=a~b~c", tokAda, ""},
		"a default without name": {"POST", base + "/default", tokAda, `{}`},
	} {
		if resp, _ := p.call(t, c.method, c.path, c.token, "", c.body); resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
	}
	if count() != before {
		t.Errorf("payments-worker was bothered %d times", count()-before)
	}

	// The audit trail: a page of events with the token for the next, kinds checked, any member reads.
	resp, m = p.call(t, "GET", "/app/v1/apps/"+adaApp+"/integrations/audit?kind=einvoice&page_size=5&page_token=20", tokViewer, "", "")
	events, _ := m["events"].([]any)
	if resp.StatusCode != 200 || len(events) != 1 || m["next_page_token"] != "9" {
		t.Fatalf("audit: %d %v", resp.StatusCode, m)
	}
	if w := last(); w.path != "/admin/tenants/"+adaLive+"/provider-account-events" || w.query != "before=20&kind=einvoice&limit=5" {
		t.Errorf("audit asked %+v", w)
	}
	if resp, _ := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/integrations/audit?kind=mail", tokAda, "", ""); resp.StatusCode != 400 {
		t.Errorf("a bad kind: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "GET", "/app/v1/apps/"+bobApp+"/integrations/audit", tokAda, "", ""); resp.StatusCode != 404 {
		t.Errorf("another account's app: %d", resp.StatusCode)
	}
}

// The tester of the integrations page: which live account would take a customer of a country paying in a currency. Any member asks, for the app's live tenant.
func TestLiveRoutingPreview(t *testing.T) {
	p := newProduct(t, nil, nil)
	path := "/app/v1/apps/" + adaApp + "/go-live/routing/preview"
	resp, m := p.call(t, "POST", path, tokViewer, "", `{"country":"DE","currency":"EUR"}`)
	if resp.StatusCode != 200 || m["provider"] != "paddle~eu" || m["can_charge"] != true {
		t.Fatalf("preview: %d %v", resp.StatusCode, m)
	}
	if got := p.plans.tenants[len(p.plans.tenants)-1]; got != adaLive {
		t.Errorf("asked for tenant %q, want the app's live tenant %q", got, adaLive)
	}
	if _, m := p.call(t, "POST", path, tokAda, "", `{"country":"FR","currency":"EUR"}`); m["provider"] != "" && m["provider"] != nil {
		t.Errorf("no rule: %v", m)
	}
	if resp, m := p.call(t, "POST", "/app/v1/apps/"+bobApp+"/go-live/routing/preview", tokAda, "", `{}`); resp.StatusCode != 404 || errKeyOf(m) != "identity.app.not_found" {
		t.Errorf("another account's app: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", path, "", "", `{}`); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
}
