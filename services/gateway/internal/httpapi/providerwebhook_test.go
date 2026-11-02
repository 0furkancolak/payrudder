package httpapi

import (
	"strings"
	"testing"
)

// The owner of an app asks for the webhook endpoint of a provider account to be made at the provider: the address is the gateway's own for the app's tenant and the
// environment of the route, whatever the caller sends; only the owner may; nothing reaches payments-worker for anyone else.
func TestProviderWebhookRegistrationRoutes(t *testing.T) {
	pw := newFakePayments(t)
	deps := pw.deps()
	p := newProduct(t, nil, func(d *Deps) { deps(d); d.PublicURL = "https://api.example.test/" })

	for _, c := range []struct{ path, tenant, env string }{
		{"/app/v1/apps/" + adaApp + "/sandbox/providers/stripe/webhook?environment=live&tenant=x", adaSandbox, "sandbox"},
		{"/app/v1/apps/" + adaApp + "/go-live/providers/stripe/webhook", adaLive, "live"},
	} {
		resp, _ := p.call(t, "POST", c.path, tokAda, "", `{"url":"https://evil.example/hook"}`)
		if resp.StatusCode/100 != 2 || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d", c.path, resp.StatusCode)
		}
		w := pw.writes()
		got := w[len(w)-1]
		if got.method != "POST" || got.path != "/admin/tenants/"+c.tenant+"/provider-accounts/stripe/webhook-endpoint" || got.query.Get("environment") != c.env || len(got.query) != 1 {
			t.Errorf("forwarded to %s %s?%v", got.method, got.path, got.query)
		}
		if want := `{"url":"https://api.example.test/webhooks/stripe/` + c.tenant + `/` + c.env + `"}`; got.body != want {
			t.Errorf("body = %s, want %s: the caller does not decide the address", got.body, want)
		}
		if got.actor != "user:"+adaUser {
			t.Errorf("actor %q", got.actor)
		}
	}

	before := len(pw.writes())
	if resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/sandbox/providers/stripe/webhook", tokViewer, "", ``); resp.StatusCode != 403 || errKeyOf(m) != "identity.go_live.owner_only" {
		t.Errorf("viewer: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "POST", "/app/v1/apps/"+bobApp+"/go-live/providers/stripe/webhook", tokAda, "", ``); resp.StatusCode != 404 || errKeyOf(m) != "identity.app.not_found" {
		t.Errorf("another account's app: %d %v", resp.StatusCode, m)
	}
	for _, name := range []string{"mockpsp", "adyen"} {
		if resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/go-live/providers/"+name+"/webhook", tokAda, "", ``); resp.StatusCode != 404 || errKeyOf(m) != "payments.provider.unknown" {
			t.Errorf("provider %q: %d %v", name, resp.StatusCode, m)
		}
	}
	if after := len(pw.writes()); after != before {
		t.Errorf("%d requests reached payments-worker for people who may not register an endpoint", after-before)
	}
}

// Stripe has one host for test and live keys: its sandbox connection is the default host with a test key, like the other providers' test hosts.
func TestStripeCanBeConnectedToTheSandbox(t *testing.T) {
	pw := newFakePayments(t)
	p := newProduct(t, nil, pw.deps())
	resp, _ := p.call(t, "PUT", "/app/v1/apps/"+adaApp+"/sandbox/providers/stripe", tokAda, "", `{"api_key":"sk_test_abc"}`)
	w := pw.writes()
	if resp.StatusCode != 200 || len(w) != 1 || w[0].path != "/admin/tenants/"+adaSandbox+"/provider-accounts/stripe" || w[0].query.Get("environment") != "sandbox" ||
		!strings.Contains(w[0].body, `"base_url":"https://api.stripe.com"`) || !strings.Contains(w[0].body, `"api_key":"sk_test_abc"`) {
		t.Fatalf("connect: %d %+v", resp.StatusCode, w)
	}
}
