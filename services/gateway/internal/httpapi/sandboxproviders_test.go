package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

// A person's sandbox provider account: the tenant and environment are the app's sandbox ones, whatever the request says; the endpoint is the provider's test host
// unless the body names one; the provider is asked to vouch; the audit names the person; the first account becomes the default; only the owner changes anything;
// and a provider without a test mode is refused before payments-worker hears of it.
func TestSandboxProviderRoutes(t *testing.T) {
	pw := newFakePayments(t)
	p := newProduct(t, nil, pw.deps())
	settings := `{"api_key":"sandbox-api","secret_key":"SECRET-ONE","buyer":{"name":"Ada"}}`

	resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/sandbox/providers/iyzico?environment=live&verify=false&default=false&tenant=x", tokAda, "", settings)
	if resp.StatusCode != 200 || m["provider"] != "iyzico" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("connect: %d %v", resp.StatusCode, m)
	}
	w := pw.writes()
	if len(w) != 1 {
		t.Fatalf("payments-worker was written %d times", len(w))
	}
	got := w[0]
	if got.method != "PUT" || got.path != "/admin/tenants/"+adaSandbox+"/provider-accounts/iyzico" {
		t.Errorf("forwarded to %s %s: the tenant is the app's sandbox one", got.method, got.path)
	}
	if got.query.Get("environment") != "sandbox" || got.query.Get("verify") != "true" || got.query.Get("default") != "true" || len(got.query) != 3 {
		t.Errorf("query = %v: sandbox, verified, the first account is the default, and nothing the caller added", got.query)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got.body), &sent); err != nil || sent["base_url"] != "https://sandbox-api.iyzipay.com" || sent["secret_key"] != "SECRET-ONE" {
		t.Errorf("body = %q: the provider's test host is filled in and the credentials arrive as typed", got.body)
	}
	if got.actor != "user:"+adaUser || got.token == "Bearer "+tokAda {
		t.Errorf("actor %q token %q", got.actor, got.token)
	}

	// A host the person names is passed on as it is: payments-worker refuses what is not the provider's test host.
	p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/sandbox/providers/paddle", tokAda, "", `{"api_key":"pdl_live_apikey_1","base_url":"https://api.paddle.com"}`)
	if w := pw.writes(); !strings.Contains(w[1].body, `"https://api.paddle.com"`) || w[1].path != "/admin/tenants/"+adaSandbox+"/provider-accounts/paddle" {
		t.Errorf("paddle forwarded as %+v", w[1])
	}

	// With a default account already, a second one does not take the place.
	pw.mu.Lock()
	pw.accounts = []map[string]any{{"provider": "iyzico", "default": true, "working": true}}
	pw.mu.Unlock()
	p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/sandbox/providers/paddle", tokAda, "", `{}`)
	if w := pw.writes(); w[2].query.Get("default") != "false" {
		t.Errorf("default = %q: a second account must not take the default from the first", w[2].query.Get("default"))
	}

	// Not the owner, not the app's member, not a provider with a test mode: nothing is forwarded.
	before := len(pw.writes())
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/sandbox/providers/iyzico", tokViewer, "", settings); resp.StatusCode != 403 || errKeyOf(m) != "identity.go_live.owner_only" {
		t.Errorf("viewer: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+bobApp+"/sandbox/providers/iyzico", tokAda, "", settings); resp.StatusCode != 404 || errKeyOf(m) != "identity.app.not_found" {
		t.Errorf("another account's app: %d %v", resp.StatusCode, m)
	}
	for _, name := range []string{"mockpsp", "adyen", "..%2Fx"} {
		if resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/sandbox/providers/"+name, tokAda, "", settings); resp.StatusCode != 404 || errKeyOf(m) != "payments.provider.unknown" {
			t.Errorf("provider %q: %d %v", name, resp.StatusCode, m)
		}
	}
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/sandbox/providers/banktransfer", tokAda, "", `{}`); resp.StatusCode != 400 || errKeyOf(m) != "payments.provider.not_supported" {
		t.Errorf("bank transfer: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/sandbox/providers/iyzico", tokAda, "", `[1]`); resp.StatusCode != 400 || errKeyOf(m) != "payments.account.bad_settings" {
		t.Errorf("a body that is not an object: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "DELETE", "/app/v1/apps/"+adaApp+"/sandbox/providers/iyzico", tokViewer, "", ``); resp.StatusCode != 403 {
		t.Errorf("viewer delete: %d", resp.StatusCode)
	}
	if after := len(pw.writes()); after != before {
		t.Errorf("%d requests reached payments-worker for people who may not connect a provider", after-before)
	}

	// Removing.
	if resp, _ := p.call(t, "DELETE", "/app/v1/apps/"+adaApp+"/sandbox/providers/iyzico?environment=live", tokAda, "", ``); resp.StatusCode != 204 {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	if last := pw.writes()[len(pw.writes())-1]; last.method != "DELETE" || last.query.Get("environment") != "sandbox" || last.actor != "user:"+adaUser || last.path != "/admin/tenants/"+adaSandbox+"/provider-accounts/iyzico" {
		t.Errorf("delete forwarded as %+v", last)
	}

	// The provider's refusal reaches the person with its key, and not the secret.
	pw.mu.Lock()
	pw.status = 400
	pw.mu.Unlock()
	if resp, m := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/sandbox/providers/iyzico", tokAda, "", settings); resp.StatusCode != 400 || errKeyOf(m) != "payments.account.health_check_failed" || strings.Contains(m2s(m), "SECRET-ONE") {
		t.Errorf("refused credentials: %d %v", resp.StatusCode, m)
	}
}

// Any member reads the sandbox providers, from the sandbox tenant; a stranger does not; without payments-worker the answer is unavailable.
func TestSandboxProvidersAreReadByMembers(t *testing.T) {
	pw := newFakePayments(t)
	pw.accounts = []map[string]any{{"provider": "paddle", "default": true, "working": true, "checked": true}}
	p := newProduct(t, nil, pw.deps())
	for _, tok := range []string{tokAda, tokViewer} {
		resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/sandbox/providers", tok, "", "")
		accounts, _ := m["accounts"].([]any)
		if resp.StatusCode != 200 || len(accounts) != 1 || resp.Header.Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s: %d %v", tok, resp.StatusCode, m)
		}
	}
	pw.mu.Lock()
	defer pw.mu.Unlock()
	last := pw.seen[len(pw.seen)-1]
	if last.path != "/admin/tenants/"+adaSandbox+"/provider-health" || last.query.Get("environment") != "sandbox" {
		t.Errorf("read %s?%v: the sandbox tenant's health, in the sandbox", last.path, last.query)
	}
	if resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/sandbox/providers", tokBob, "", ""); resp.StatusCode != 404 || errKeyOf(m) != "identity.app.not_found" {
		t.Errorf("a stranger: %d %v", resp.StatusCode, m)
	}
	down := newProduct(t, nil, nil)
	if resp, m := down.call(t, "GET", "/app/v1/apps/"+adaApp+"/sandbox/providers", tokAda, "", ""); resp.StatusCode != 503 || errKeyOf(m) != "gateway.payments.unreachable" {
		t.Errorf("without payments-worker: %d %v", resp.StatusCode, m)
	}
}
