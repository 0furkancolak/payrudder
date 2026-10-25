package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Phase 45 G: the dev seed lives in the sandbox. A new app has a live tenant too (inactive, without keys), and the seed must never touch it: no live tenant gets
// data, no live key is made, nothing goes live, nothing is promoted. The whole seed runs here against a stand-in gateway that has both tenants for every
// app and writes down every request it gets.

const (
	liveTenantID    = "11111111-1111-4111-8111-000000000001"
	sandboxTenantID = "11111111-1111-4111-8111-000000000002"
)

type recorded struct{ method, path, tenant string }

// devGateway answers the product calls the seed makes (sign in, the app list, creating an app) and gives everything else to the tenant stand-in.
type devGateway struct {
	mu     sync.Mutex
	stack  *fakeStack
	seen   []recorded
	apps   []map[string]any
	signed int
}

func (g *devGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.seen = append(g.seen, recorded{r.Method, r.URL.Path, r.Header.Get("X-Payrudder-Tenant")})
	g.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(strings.NewReader(string(raw)))
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/admin/v1/tenants":
		_, _ = w.Write([]byte(`{"tenants":[]}`))
	case r.URL.Path == "/auth/signin":
		var in map[string]string
		_ = json.Unmarshal(raw, &in)
		g.mu.Lock()
		g.signed++
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":       "qs_" + in["email"],
			"user":        map[string]any{"id": "u", "email": in["email"], "email_verified": true},
			"memberships": []map[string]any{{"account_id": "acc-" + in["email"], "account_name": accountOf(in["email"]), "role": "owner"}},
		})
	case r.URL.Path == "/app/v1/apps" && r.Method == http.MethodGet:
		g.mu.Lock()
		defer g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"apps": g.apps})
	case r.URL.Path == "/app/v1/apps" && r.Method == http.MethodPost:
		var in map[string]string
		_ = json.Unmarshal(raw, &in)
		app := map[string]any{"id": "app-" + in["name"], "name": in["name"], "tenants": []map[string]any{
			{"id": liveTenantID, "environment": "ENVIRONMENT_LIVE", "status": "TENANT_STATUS_INACTIVE"},
			{"id": sandboxTenantID, "environment": "ENVIRONMENT_SANDBOX", "status": "TENANT_STATUS_ACTIVE"},
		}}
		g.mu.Lock()
		g.apps = append(g.apps, app)
		g.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(app)
	default:
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		g.stack.ServeHTTP(w, r)
	}
}

func accountOf(email string) string {
	for _, p := range devPeople {
		if p.Email == email {
			return p.Account
		}
	}
	return ""
}

func TestDevSeedNeverCreatesALiveTenantOrKey(t *testing.T) {
	gw := &devGateway{stack: newFakeStack()}
	srv := httptest.NewServer(gw)
	defer srv.Close()
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	err := runDevSeed(devSeedOptions{Gateway: srv.URL, Token: "x", Out: io.Discard, HTTP: srv.Client(), Now: func() time.Time { return now }, PollWait: time.Millisecond, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(gw.seen) < 50 {
		t.Fatalf("the seed made only %d requests: the test is not looking at a real run", len(gw.seen))
	}
	tenantCalls := 0
	for _, c := range gw.seen {
		// Nothing that makes a key, switches live on, promotes configuration or resets: the seed fills the sandbox with data and does nothing else to the environments.
		for _, bad := range []string{"/api-keys", "/go-live", "/promote", "/sandbox/reset"} {
			if strings.Contains(c.path, bad) {
				t.Errorf("%s %s: the dev seed must not call %s", c.method, c.path, bad)
			}
		}
		if c.tenant == liveTenantID {
			t.Errorf("%s %s was sent as the live tenant", c.method, c.path)
		}
		if c.tenant != "" {
			tenantCalls++
			if c.tenant != sandboxTenantID {
				t.Errorf("%s %s was sent as tenant %q, not the sandbox", c.method, c.path, c.tenant)
			}
		}
		// An app is made by name, currency and time zone only: there is no way to ask the product API for a live tenant with data or a key.
		if c.method == http.MethodPost && strings.HasPrefix(c.path, "/admin/v1/accounts") {
			t.Errorf("%s %s: accounts are made through sign-up, not the operator API", c.method, c.path)
		}
	}
	if tenantCalls == 0 {
		t.Fatal("no request carried a tenant: the seed did not fill anything")
	}
}

func TestSandboxTenantIsNeverTheLiveOne(t *testing.T) {
	var a appJSON
	raw := `{"id":"a","name":"x","tenants":[{"id":"L","environment":"ENVIRONMENT_LIVE"},{"id":"S","environment":"ENVIRONMENT_SANDBOX"}]}`
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	if got := a.sandboxTenant(); got != "S" {
		t.Errorf("sandboxTenant = %q, want S", got)
	}
	// An app with only a live tenant is refused rather than seeded.
	live := appJSON{}
	if err := json.Unmarshal([]byte(`{"tenants":[{"id":"L","environment":"ENVIRONMENT_LIVE"}]}`), &live); err != nil {
		t.Fatal(err)
	}
	if got := live.sandboxTenant(); got != "" {
		t.Errorf("an app with no sandbox gave tenant %q: the seed would write to live", got)
	}
}
