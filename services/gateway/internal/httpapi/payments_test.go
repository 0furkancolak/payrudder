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

type seen struct {
	method, path, query, token, authorization, body, actor string
}

func paymentsGateway(t *testing.T, token string) (*httptest.Server, *seen) {
	t.Helper()
	got := &seen{}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Internal-Token"), r.Header.Get("Authorization"), string(b), r.Header.Get("X-Quotify-Actor")}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(worker.Close)
	u, _ := url.Parse(worker.URL)
	api := New(Deps{Auth: fakeAuth{}, Limiter: &fakeLimiter{}, OperatorToken: operatorToken, PaymentsURL: u, PaymentsToken: token,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	return ts, got
}

func TestProviderWebhookIsPublicAndForwardedAsIs(t *testing.T) {
	ts, got := paymentsGateway(t, "internal-secret")
	resp, _ := do(t, ts, "POST", "/webhooks/iyzico", "", `{"iyziReferenceCode":"r"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got.method != "POST" || got.path != "/webhooks/iyzico" || got.body != `{"iyziReferenceCode":"r"}` {
		t.Errorf("worker saw %+v", *got)
	}
	// Whatever a caller sends as the internal token never reaches the worker's webhook with their value.
	req, _ := http.NewRequest("POST", ts.URL+"/webhooks/iyzico", strings.NewReader(`{}`))
	req.Header.Set("X-Internal-Token", "forged")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("%v", err)
	}
	if got.token != "internal-secret" {
		t.Errorf("worker saw token %q; the gateway must set its own", got.token)
	}
}

func TestProviderAccountRoutesNeedTheOperatorAndAreRewritten(t *testing.T) {
	ts, got := paymentsGateway(t, "internal-secret")
	tenantID := "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e"
	path := "/admin/v1/tenants/" + tenantID + "/provider-accounts/iyzico?environment=live&default=true"

	if resp, _ := do(t, ts, "PUT", path, "live-key", `{}`); resp.StatusCode != 401 {
		t.Errorf("a tenant key on an operator route: %d", resp.StatusCode)
	}
	if resp, _ := do(t, ts, "PUT", path, "", `{}`); resp.StatusCode != 401 {
		t.Errorf("no credentials: %d", resp.StatusCode)
	}
	if got.method != "" {
		t.Fatal("the worker was reached without an operator")
	}
	resp, _ := do(t, ts, "PUT", path, operatorToken, `{"api_key":"k"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got.method != "PUT" || got.path != "/admin/tenants/"+tenantID+"/provider-accounts/iyzico" || got.query != "environment=live&default=true" ||
		got.token != "internal-secret" || got.authorization != "" || got.body != `{"api_key":"k"}` {
		t.Errorf("worker saw %+v; the path is rewritten, the operator's Authorization is dropped and the internal token is set", *got)
	}
	do(t, ts, "GET", "/admin/v1/tenants/"+tenantID+"/ledger?limit=5", operatorToken, "")
	if got.path != "/admin/tenants/"+tenantID+"/ledger" || got.query != "limit=5" {
		t.Errorf("ledger: worker saw %+v", *got)
	}
}

func TestTenantPaymentsAreTheCallersOwnLedger(t *testing.T) {
	ts, got := paymentsGateway(t, "internal-secret")
	if resp, _ := do(t, ts, "GET", "/v1/payments?limit=5", "", ""); resp.StatusCode != 401 || got.method != "" {
		t.Fatalf("no key reached the worker: %d %+v", resp.StatusCode, *got)
	}
	// A tenant's key reads its own ledger, whatever tenant the query names; the caller's Authorization never reaches the worker.
	resp, _ := do(t, ts, "GET", "/v1/payments?limit=5&tenant_id="+sandboxTenant.String(), "live-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got.path != "/admin/tenants/"+liveTenant.String()+"/ledger" || got.authorization != "" || got.token != "internal-secret" {
		t.Errorf("worker saw %+v", *got)
	}
}

func TestTransferRoutesAreOperatorOnlyAndForwardedWithTheActor(t *testing.T) {
	ts, got := paymentsGateway(t, "internal-secret")
	base := "/admin/v1/tenants/0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e/invoices/0192f0b4-0000-7c3a-9a3e-6a1f2b3c4d5e/transfer"
	if resp, _ := do(t, ts, "POST", base+"/receipts", "live-key", `{}`); resp.StatusCode != 401 || got.method != "" {
		t.Fatalf("a tenant key reached the worker: %d %+v", resp.StatusCode, *got)
	}
	for _, c := range []struct{ method, tail, body string }{{"GET", "", ""}, {"POST", "/receipts", `{"amount_minor":1}`}, {"POST", "/reject", `{"outcome":"expired"}`}} {
		req, _ := http.NewRequest(c.method, ts.URL+base+c.tail, strings.NewReader(c.body))
		req.Header.Set("Authorization", "Bearer "+operatorToken)
		req.Header.Set("X-Quotify-Actor", "alice@example.com")
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s %s: %v %v", c.method, c.tail, resp, err)
		}
		_ = resp.Body.Close()
		if got.method != c.method || got.path != strings.Replace(base, "/admin/v1", "/admin", 1)+c.tail || got.body != c.body || got.token != "internal-secret" || got.authorization != "" || got.actor != "alice@example.com" {
			t.Errorf("%s %s: worker saw %+v", c.method, c.tail, *got)
		}
	}
}

func TestPaymentsRoutesWithoutAWorker(t *testing.T) {
	api := New(Deps{Auth: fakeAuth{}, Limiter: &fakeLimiter{}, OperatorToken: operatorToken, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ts := httptest.NewServer(api)
	defer ts.Close()
	resp, _ := do(t, ts, "POST", "/webhooks/iyzico", "", `{}`)
	if resp.StatusCode != 503 {
		t.Errorf("webhook with no worker configured: %d, want 503", resp.StatusCode)
	}
}
