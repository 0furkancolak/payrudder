package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestScopeOfAKeyLimitsWhatItCanCall(t *testing.T) {
	ts, _ := newTestServer(t)
	body := `{"external_id":"c1","name":"Ada","email":"ada@example.com"}`
	for name, tc := range map[string]struct {
		key, method, path string
		want              int // 403 is the scope check; 0 means "past it" (the fake upstream answers whatever it answers)
	}{
		"read key lists":                  {"read-key", "GET", "/v1/customers", 0},
		"read key cannot create":          {"read-key", "POST", "/v1/customers", 403},
		"read key cannot send events":     {"read-key", "POST", "/v1/events", 403},
		"read key cannot cancel":          {"read-key", "DELETE", "/v1/subscriptions/00000000-0000-4000-8000-000000000000", 403},
		"ingest key cannot create":        {"ingest-key", "POST", "/v1/customers", 403},
		"ingest key cannot read":          {"ingest-key", "GET", "/v1/customers", 403},
		"ingest key cannot read invoices": {"ingest-key", "GET", "/v1/invoices", 403},
		"full key creates":                {"sandbox-key", "POST", "/v1/customers", 0},
	} {
		resp, m := do(t, ts, tc.method, tc.path, tc.key, body)
		if (resp.StatusCode == 403) != (tc.want == 403) {
			t.Errorf("%s: status %d (%v), want 403: %v", name, resp.StatusCode, m, tc.want == 403)
		}
		if tc.want == 403 && errKey(m) != "gateway.auth.scope_denied" {
			t.Errorf("%s: key %q, want gateway.auth.scope_denied", name, errKey(m))
		}
	}
	// An ingest key does what it is for: the request gets past the scope check (an empty batch is the handler's own 400, not the gateway's 403).
	resp, m := do(t, ts, "POST", "/v1/events", "ingest-key", `{"events":[]}`)
	if resp.StatusCode == 403 {
		t.Errorf("ingest key refused on /v1/events: %v", m)
	}
}

func TestSuspendedTenantIsTold(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "GET", "/v1/customers", "suspended-key", "")
	if resp.StatusCode != 403 || errKey(m) != "gateway.auth.tenant_not_active" {
		t.Fatalf("suspended: %d %v", resp.StatusCode, m)
	}
	// A key that is simply wrong learns nothing about tenants.
	resp, m = do(t, ts, "GET", "/v1/customers", "nope", "")
	if resp.StatusCode != 401 || errKey(m) == "gateway.auth.tenant_not_active" {
		t.Fatalf("bad key: %d %v", resp.StatusCode, m)
	}
}

// The environment of a call is the key's tenant's, whatever the caller writes in a header or the query.
func TestEnvironmentComesFromTheKeyNeverFromTheRequest(t *testing.T) {
	ts, f := newTestServer(t)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/customers?environment=live", strings.NewReader(`{"external_id":"c1","name":"Ada","email":"ada@example.com"}`))
	req.Header.Set("Authorization", "Bearer sandbox-key")
	for _, h := range []string{"X-Payrudder-Environment", "x-payrudder-environment", "X-Environment", "X-Payrudder-Tenant-Id"} {
		req.Header.Set(h, "live")
	}
	req.Header.Set("X-Payrudder-Tenant-Id", liveTenant.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if f.lastTenant.Environment != "sandbox" || f.lastTenant.ID != sandboxTenant {
		t.Errorf("upstream saw %+v; a sandbox key must stay in the sandbox tenant", f.lastTenant)
	}
}
