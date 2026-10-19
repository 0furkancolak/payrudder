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
	method, path, query, token, authorization, body string
}

func paymentsGateway(t *testing.T, token string) (*httptest.Server, *seen) {
	t.Helper()
	got := &seen{}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Internal-Token"), r.Header.Get("Authorization"), string(b)}
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

func TestPaymentsRoutesWithoutAWorker(t *testing.T) {
	api := New(Deps{Auth: fakeAuth{}, Limiter: &fakeLimiter{}, OperatorToken: operatorToken, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ts := httptest.NewServer(api)
	defer ts.Close()
	resp, _ := do(t, ts, "POST", "/webhooks/iyzico", "", `{}`)
	if resp.StatusCode != 503 {
		t.Errorf("webhook with no worker configured: %d, want 503", resp.StatusCode)
	}
}
