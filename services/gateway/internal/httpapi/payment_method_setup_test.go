package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// setupWorker is a payments-worker that records the call it got and answers what the test says.
type setupWorker struct {
	calls                     int
	method, path, query, body string
	token, audited            string
	status                    int
	answer                    string
	onRequest                 func(*http.Request)
}

func setupGateway(t *testing.T, w *setupWorker) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.calls++
		if w.onRequest != nil {
			w.onRequest(r)
		}
		w.method, w.path, w.query, w.body, w.token, w.audited = r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("X-Internal-Token"), r.Header.Get("Authorization")
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(w.status)
		_, _ = io.WriteString(rw, w.answer)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) {
		d.PaymentsURL, d.PaymentsToken, d.Log = u, "internal-secret", slog.New(slog.NewTextHandler(io.Discard, nil))
	})
	return ts
}

// The seller asks for the link to the provider's page where a customer saves a card: it goes to payments-worker for the caller's own tenant and environment, with the customer of
// the path and nothing of the body but the address the customer returns to; the worker's answer and its refusals come back as they are, and what the gateway can refuse (a
// provider that has no such page, an address that is not https, a read-only key, a frozen tenant) it refuses before the worker is asked.
func TestAskingForACardGoesToTheCallersOwnTenantAndEnvironment(t *testing.T) {
	w := &setupWorker{status: 201, answer: `{"url":"https://checkout.stripe.com/c/pay/cs_test_1","ref":"cs_test_1"}`}
	ts := setupGateway(t, w)

	resp, m := do(t, ts, "POST", "/v1/customers/cus_1/payment-method-setup", "live-key", `{"return_url":"https://shop.example.com/thanks","tenant_id":"someone-else","environment":"sandbox"}`)
	if resp.StatusCode != 201 || m["url"] != "https://checkout.stripe.com/c/pay/cs_test_1" || m["ref"] != "cs_test_1" {
		t.Fatalf("live: %d %v", resp.StatusCode, m)
	}
	if want := "/admin/tenants/" + liveTenant.String() + "/providers/stripe/customers/cus_1/payment-method-setup"; w.method != "POST" || w.path != want || w.query != "environment=live" {
		t.Errorf("the worker was asked %s %s?%s; want POST %s?environment=live (the tenant and environment of the key, never the body's)", w.method, w.path, w.query, want)
	}
	if w.body != `{"return_url":"https://shop.example.com/thanks"}` {
		t.Errorf("the worker was sent %q; want the return address alone", w.body)
	}
	if w.token != "internal-secret" || w.audited != "" {
		t.Errorf("the worker saw token %q and Authorization %q: the gateway sets its own token and sends none of the caller's", w.token, w.audited)
	}

	// A sandbox key asks in the sandbox; the provider can be named, in any case.
	if resp, _ := do(t, ts, "POST", "/v1/customers/cus_2/payment-method-setup", "sandbox-key", `{"provider":" Stripe ","return_url":"https://shop.example.com/thanks"}`); resp.StatusCode != 201 {
		t.Fatalf("sandbox: %d", resp.StatusCode)
	}
	if want := "/admin/tenants/" + sandboxTenant.String() + "/providers/stripe/customers/cus_2/payment-method-setup"; w.path != want || w.query != "environment=sandbox" {
		t.Errorf("sandbox: the worker was asked %s?%s; want %s?environment=sandbox", w.path, w.query, want)
	}
	// A customer id with a slash is one name, not a path: the worker's router reads it back whole.
	var raw string
	w.onRequest = func(r *http.Request) { raw = r.URL.EscapedPath() }
	if resp, _ := do(t, ts, "POST", "/v1/customers/a%2Fb/payment-method-setup", "live-key", `{"return_url":"https://shop.example.com/thanks"}`); resp.StatusCode != 201 || !strings.HasSuffix(raw, "/customers/a%2Fb/payment-method-setup") {
		t.Errorf("a customer id with a slash: %d, the worker was sent %q", resp.StatusCode, raw)
	}

	before := w.calls
	for _, tc := range []struct{ name, key, body string }{
		{"a provider with no such page", "live-key", `{"provider":"paddle","return_url":"https://shop.example.com/thanks"}`},
		{"a path in the provider", "live-key", `{"provider":"stripe/../x","return_url":"https://shop.example.com/thanks"}`},
		{"an address that is not https", "live-key", `{"return_url":"http://shop.example.com/thanks"}`},
		{"no address", "live-key", `{}`},
		{"an address with no host", "live-key", `{"return_url":"https:///thanks"}`},
		{"a javascript address", "live-key", `{"return_url":"javascript:alert(1)"}`},
		{"an address of 2001 characters", "live-key", `{"return_url":"https://shop.example.com/` + strings.Repeat("a", 2000) + `"}`},
		{"a body that is not JSON", "live-key", `not json`},
	} {
		if resp, m := do(t, ts, "POST", "/v1/customers/cus_1/payment-method-setup", tc.key, tc.body); resp.StatusCode != 400 || errCode(m) != "INVALID_ARGUMENT" {
			t.Errorf("%s: %d %v; want 400", tc.name, resp.StatusCode, m)
		}
	}
	for _, tc := range []struct{ key, want string }{{"read-key", "gateway.auth.permission_denied"}, {"custom-key", "gateway.auth.permission_denied"}, {"frozen-key", "gateway.auth.tenant_read_only"}} {
		if resp, m := do(t, ts, "POST", "/v1/customers/cus_1/payment-method-setup", tc.key, `{"return_url":"https://shop.example.com/thanks"}`); resp.StatusCode != 403 || errKeyOf(m) != tc.want {
			t.Errorf("%s: %d %v; want 403 %s", tc.key, resp.StatusCode, m, tc.want)
		}
	}
	if w.calls != before {
		t.Errorf("the worker was asked %d times for what the gateway refuses", w.calls-before)
	}

	// The worker's own refusals come back with their status and key.
	w.status, w.answer = 404, `{"error":{"code":"NOT_FOUND","key":"payments.customer.not_ready","message":"no such customer"}}`
	if resp, m := do(t, ts, "POST", "/v1/customers/nobody/payment-method-setup", "live-key", `{"return_url":"https://shop.example.com/thanks"}`); resp.StatusCode != 404 || errKeyOf(m) != "payments.customer.not_ready" {
		t.Errorf("a customer that does not exist: %d %v", resp.StatusCode, m)
	}
	w.status, w.answer = 501, `{"error":{"code":"UNIMPLEMENTED","key":"payments.provider.not_supported","message":"stripe does not support saving a payment method"}}`
	if resp, m := do(t, ts, "POST", "/v1/customers/cus_1/payment-method-setup", "live-key", `{"return_url":"https://shop.example.com/thanks"}`); resp.StatusCode != 501 || errKeyOf(m) != "payments.provider.not_supported" {
		t.Errorf("a provider that cannot: %d %v", resp.StatusCode, m)
	}
}

// Without a payments-worker the route says so, as every other route that needs one.
func TestAskingForACardWithoutAPaymentsWorker(t *testing.T) {
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) { d.PaymentsURL = nil })
	if resp, m := do(t, ts, "POST", "/v1/customers/cus_1/payment-method-setup", "live-key", `{"return_url":"https://shop.example.com/thanks"}`); resp.StatusCode != 503 || errKeyOf(m) != "gateway.payments.not_configured" {
		t.Errorf("no payments-worker: %d %v", resp.StatusCode, m)
	}
}
