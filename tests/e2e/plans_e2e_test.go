//go:build e2e

// Package e2e runs against the compose stack (make up): real gateway, plans-svc, Postgres and Redis.
//
//	make up && make e2e
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

var (
	gateway       = envOr("PAYRUDDER_GATEWAY_URL", "http://localhost:42800")
	operatorToken = envOr("PAYRUDDER_OPERATOR_TOKEN", "local-operator-token-change-me-0123456789")
	httpc         = &http.Client{Timeout: 10 * time.Second}
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type result struct {
	status int
	header http.Header
	body   map[string]any
}

func call(t *testing.T, method, path, token string, body any) result {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, gateway+path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out := result{status: resp.StatusCode, header: resp.Header}
	_ = json.NewDecoder(resp.Body).Decode(&out.body)
	return out
}

func (r result) errCode() string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

type account struct {
	id         string
	sandbox    string
	live       string
	sandboxKey string
	liveKey    string
	liveKeyID  string
}

func newAccount(t *testing.T, tier string) account {
	t.Helper()
	r := call(t, "POST", "/admin/v1/accounts", operatorToken, map[string]any{
		"name": "E2E " + t.Name(), "currency": "TRY", "timezone": "Europe/Istanbul", "grace_period": "86400s", "tier": tier,
	})
	if r.status != 201 {
		t.Fatalf("create account: %d %v", r.status, r.body)
	}
	a := account{id: r.body["id"].(string)}
	for _, raw := range r.body["tenants"].([]any) {
		tn := raw.(map[string]any)
		if tn["environment"] == "ENVIRONMENT_LIVE" {
			// A live tenant is created inactive and has no keys: activate it the way support does, with a reason.
			if s := call(t, "PUT", "/admin/v1/tenants/"+tn["id"].(string)+"/status", operatorToken, map[string]any{"status": "active", "reason": "e2e"}); s.status != 200 {
				t.Fatalf("activate: %d %v", s.status, s.body)
			}
		}
		k := call(t, "POST", "/admin/v1/tenants/"+tn["id"].(string)+"/api-keys", operatorToken, nil)
		if k.status != 201 {
			t.Fatalf("create key: %d %v", k.status, k.body)
		}
		key := k.body["plaintext"].(string)
		switch tn["environment"] {
		case "ENVIRONMENT_SANDBOX":
			a.sandbox, a.sandboxKey = tn["id"].(string), key
		case "ENVIRONMENT_LIVE":
			a.live, a.liveKey = tn["id"].(string), key
			a.liveKeyID = k.body["api_key"].(map[string]any)["id"].(string)
		}
	}
	return a
}

func TestAccountRequiresExplicitSettings(t *testing.T) {
	r := call(t, "POST", "/admin/v1/accounts", operatorToken, map[string]any{"name": "x"})
	if r.status != 400 || r.errCode() != "INVALID_ARGUMENT" {
		t.Fatalf("got %d %v", r.status, r.body)
	}
	fields := map[string]bool{}
	for _, d := range r.body["error"].(map[string]any)["details"].([]any) {
		fields[d.(map[string]any)["field"].(string)] = true
	}
	for _, f := range []string{"currency", "timezone", "grace_period"} {
		if !fields[f] {
			t.Errorf("no violation for %s: %v", f, fields)
		}
	}
	if r := call(t, "POST", "/admin/v1/accounts", "wrong", map[string]any{"name": "x"}); r.status != 401 {
		t.Fatalf("wrong operator token: %d", r.status)
	}
}

func TestSandboxAndLiveAreIsolated(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	if !strings.HasPrefix(a.sandboxKey, "qk_test_") || !strings.HasPrefix(a.liveKey, "qk_live_") {
		t.Fatalf("keys %q %q", a.sandboxKey, a.liveKey)
	}
	if r := call(t, "POST", "/v1/customers", a.sandboxKey, map[string]any{
		"external_id": "cus_1", "name": "Ada", "email": "ada@example.com", "payment_method": "pm_sandbox_declined"}); r.status != 201 {
		t.Fatalf("sandbox create: %d %v", r.status, r.body)
	}
	if r := call(t, "GET", "/v1/customers/cus_1", a.liveKey, nil); r.status != 404 {
		t.Fatalf("live key sees sandbox customer: %d %v", r.status, r.body)
	}
	if r := call(t, "POST", "/v1/customers", a.liveKey, map[string]any{
		"external_id": "cus_1", "name": "Ada", "email": "ada@example.com", "payment_method": "pm_sandbox_success"}); r.status != 400 {
		t.Fatalf("live accepted a sandbox token: %d %v", r.status, r.body)
	}
	if r := call(t, "POST", "/v1/sandbox/customers/cus_1/payment-scenario", a.liveKey, map[string]any{"scenario": "declined"}); r.status != 403 {
		t.Fatalf("live key on sandbox route: %d", r.status)
	}
	if r := call(t, "POST", "/v1/sandbox/customers/cus_1/payment-scenario", a.sandboxKey, map[string]any{"scenario": "timeout_committed"}); r.status != 200 ||
		r.body["payment_method"] != "pm_sandbox_timeout_committed" {
		t.Fatalf("sandbox scenario: %d %v", r.status, r.body)
	}
}

func TestCatalogFlow(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.liveKey
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "gb_hours", "aggregation": "AGGREGATION_SUM", "value_property": "gb_h", "quantity_scale": 3, "unit_label": "GB-h"}},
		{"/v1/plans", map[string]any{"code": "pro", "name": "Pro"}},
		{"/v1/plans/pro/versions", map[string]any{"base_fee_minor": 9900, "prices": []any{map[string]any{
			"meter_code": "gb_hours", "tiered": map[string]any{"tiers": []any{
				map[string]any{"up_to": "10.5", "unit_price": "0"}, map[string]any{"unit_price": "12.5"}}}}},
			"entitlements": []any{map[string]any{"meter_code": "gb_hours", "limit": "100.25"}}}},
		{"/v1/customers", map[string]any{"external_id": "cus_9", "name": "N", "email": "n@example.com"}},
		{"/v1/subscriptions", map[string]any{"customer_external_id": "cus_9", "plan_code": "pro", "fee_timing": "FEE_TIMING_ARREARS"}},
	} {
		if r := call(t, "POST", step.path, k, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}
	r := call(t, "GET", "/v1/plans/pro/versions/latest", k, nil)
	if r.status != 200 || r.body["base_fee_minor"] != "9900" {
		t.Fatalf("latest version: %d %v", r.status, r.body)
	}
	ent := r.body["entitlements"].([]any)[0].(map[string]any)
	if ent["limit"] != "100.250" {
		t.Fatalf("limit = %v", ent["limit"])
	}
	if r := call(t, "POST", "/v1/subscriptions", k, map[string]any{"customer_external_id": "cus_9", "plan_code": "pro", "fee_timing": "FEE_TIMING_ARREARS"}); r.status != 409 {
		t.Fatalf("overlapping subscription: %d %v", r.status, r.body)
	}
	bad := call(t, "POST", "/v1/plans/pro/versions", k, map[string]any{"prices": []any{map[string]any{"meter_code": "gb_hours",
		"tiered": map[string]any{"tiers": []any{map[string]any{"up_to": "10", "unit_price": "1"}, map[string]any{"up_to": "5", "unit_price": "1"},
			map[string]any{"unit_price": "1"}}}}}})
	if bad.status != 400 {
		t.Fatalf("non-ascending tiers: %d %v", bad.status, bad.body)
	}
	d := bad.body["error"].(map[string]any)["details"].([]any)[0].(map[string]any)
	if d["field"] != "prices[0].tiered.tiers[1].up_to" {
		t.Fatalf("field = %v", d["field"])
	}
}

func TestRevokedKeyRejectedOnNextRequest(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	// Warm the gateway cache, then revoke.
	if r := call(t, "GET", "/v1/meters", a.liveKey, nil); r.status != 200 {
		t.Fatalf("before revoke: %d %v", r.status, r.body)
	}
	if r := call(t, "DELETE", "/admin/v1/api-keys/"+a.liveKeyID, operatorToken, nil); r.status != 200 {
		t.Fatalf("revoke: %d %v", r.status, r.body)
	}
	if r := call(t, "GET", "/v1/meters", a.liveKey, nil); r.status != 401 {
		t.Fatalf("after revoke: %d %v (cache not invalidated)", r.status, r.body)
	}
}

func TestFreeTierRateLimit(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_FREE")
	// free tier `write`: 5/s, burst 10. Fire 30 writes back to back.
	var limited int
	var retryAfter string
	for i := 0; i < 30; i++ {
		r := call(t, "POST", "/v1/plans", a.liveKey, map[string]any{"code": fmt.Sprintf("p%d", i), "name": "P"})
		if r.status == 429 {
			limited++
			retryAfter = r.header.Get("Retry-After")
			if r.errCode() != "RESOURCE_EXHAUSTED" {
				t.Fatalf("429 body: %v", r.body)
			}
		}
	}
	if limited < 10 || retryAfter == "" {
		t.Fatalf("limited %d of 30 (Retry-After %q), want ≥ 10", limited, retryAfter)
	}
}
