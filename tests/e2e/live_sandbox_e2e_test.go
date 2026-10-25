//go:build e2e

package e2e

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Phase 45 acceptance, end to end through the gateway: a person signs up, makes an app and works with its sandbox and live environments. The operator
// token is used only for what support does (switching a live environment on); everything else is the product API with a session, or a tenant API key.

func (r result) errKey() string {
	e, _ := r.body["error"].(map[string]any)
	k, _ := e["key"].(string)
	return k
}

var verifyLink = regexp.MustCompile(`qv_[A-Za-z0-9_-]{20,}`)

// newPerson signs a person up, reads the verification mail from Mailpit and spends its link, then signs in. It returns the session token and the account.
func newPerson(t *testing.T) (token, account string) {
	t.Helper()
	email := fmt.Sprintf("e2e-live-%d@example.com", time.Now().UnixNano())
	const password = "a long enough password"
	if r := call(t, "POST", "/auth/signup", "", map[string]any{"email": email, "password": password, "display_name": "E2E", "account_name": "E2E Live Co", "locale": "en"}); r.status != 202 {
		t.Fatalf("signup: %d %v", r.status, r.body)
	}
	var link string
	for deadline := time.Now().Add(30 * time.Second); link == "" && time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		for _, m := range mailTo(t, email) {
			var msg struct{ Text, HTML string }
			getJSON(t, mailpitURL+"/api/v1/message/"+url.PathEscape(m.ID), &msg)
			if link = verifyLink.FindString(msg.Text + " " + msg.HTML); link != "" {
				break
			}
		}
	}
	if link == "" {
		t.Fatal("no verification mail arrived")
	}
	if r := call(t, "POST", "/auth/verify", "", map[string]any{"token": link}); r.status != 200 {
		t.Fatalf("verify: %d %v", r.status, r.body)
	}
	in := call(t, "POST", "/auth/signin", "", map[string]any{"email": email, "password": password})
	if in.status != 200 {
		t.Fatalf("signin: %d %v", in.status, in.body)
	}
	tok, _ := in.body["token"].(string)
	ms, _ := in.body["memberships"].([]any)
	if tok == "" || len(ms) == 0 {
		t.Fatalf("signin gave %v", in.body)
	}
	return tok, ms[0].(map[string]any)["account_id"].(string)
}

func TestLiveAndSandboxAcceptance(t *testing.T) {
	session, acct := newPerson(t)

	created := call(t, "POST", "/app/v1/apps?account_id="+acct, session, map[string]any{"name": "Live and sandbox", "currency": "TRY", "timezone": "Europe/Istanbul"})
	if created.status != 201 {
		t.Fatalf("create app: %d %v", created.status, created.body)
	}
	appID, _ := created.body["id"].(string)
	var liveTenant, sandboxTenant, liveStatus string
	for _, raw := range created.body["tenants"].([]any) {
		tn := raw.(map[string]any)
		switch tn["environment"] {
		case "ENVIRONMENT_LIVE":
			liveTenant, _ = tn["id"].(string)
			liveStatus, _ = tn["status"].(string)
		case "ENVIRONMENT_SANDBOX":
			sandboxTenant, _ = tn["id"].(string)
		}
	}
	if liveTenant == "" || sandboxTenant == "" {
		t.Fatalf("app tenants: %v", created.body["tenants"])
	}
	base := "/app/v1/apps/" + appID

	t.Run("a live environment starts inactive and cannot be issued a key", func(t *testing.T) {
		if liveStatus != "TENANT_STATUS_INACTIVE" {
			t.Fatalf("a new live tenant is %q, want inactive", liveStatus)
		}
		r := call(t, "POST", base+"/api-keys", session, map[string]any{"environment": "live"})
		if r.status < 400 || r.errKey() != "plans.api_key.tenant_not_active" {
			t.Fatalf("live key on an inactive environment: %d %v", r.status, r.body)
		}
		// The operator route refuses it too: support switches the environment on first.
		if r := call(t, "POST", "/admin/v1/tenants/"+liveTenant+"/api-keys", operatorToken, nil); r.status < 400 || r.errKey() != "plans.api_key.tenant_not_active" {
			t.Fatalf("operator live key on an inactive environment: %d %v", r.status, r.body)
		}
	})

	t.Run("the go-live checklist is not complete and activation says so", func(t *testing.T) {
		g := call(t, "GET", base+"/go-live", session, nil)
		gl, _ := g.body["go_live"].(map[string]any)
		if g.status != 200 || gl["ready"] != false {
			t.Fatalf("go-live: %d %v", g.status, g.body)
		}
		if r := call(t, "POST", base+"/go-live", session, map[string]any{"accept_terms": true}); r.status != 409 || r.errKey() != "identity.go_live.incomplete" {
			t.Fatalf("activation with an incomplete list: %d %v", r.status, r.body)
		}
	})

	k := call(t, "POST", base+"/api-keys", session, map[string]any{"environment": "sandbox", "scope": "full"})
	if k.status != 201 {
		t.Fatalf("sandbox key: %d %v", k.status, k.body)
	}
	sandboxKey, _ := k.body["plaintext"].(string)
	if !strings.HasPrefix(sandboxKey, "qk_test_") {
		t.Fatalf("sandbox key %q", sandboxKey)
	}

	// Support switches live on (the checklist needs a provider and a legal profile no person can set yet), then the live key can be issued.
	if s := call(t, "PUT", "/admin/v1/tenants/"+liveTenant+"/status", operatorToken, map[string]any{"status": "active", "reason": "e2e"}); s.status != 200 {
		t.Fatalf("activate live: %d %v", s.status, s.body)
	}
	lk := call(t, "POST", base+"/api-keys", session, map[string]any{"environment": "live"})
	if lk.status != 201 {
		t.Fatalf("live key once active: %d %v", lk.status, lk.body)
	}
	liveKey, _ := lk.body["plaintext"].(string)
	if !strings.HasPrefix(liveKey, "qk_live_") {
		t.Fatalf("live key %q", liveKey)
	}

	customer := func(key, ext string) {
		t.Helper()
		body := map[string]any{"external_id": ext, "name": "Customer " + ext, "email": ext + "@example.com"}
		if key == sandboxKey {
			// A sandbox customer pays with a scenario token; a live customer takes a real payment method, so none is given here.
			body["payment_method"] = "pm_sandbox_success"
		}
		if r := call(t, "POST", "/v1/customers", key, body); r.status != 201 {
			t.Fatalf("customer %s: %d %v", ext, r.status, r.body)
		}
	}
	customer(sandboxKey, "cus_sbx")
	customer(liveKey, "cus_live")

	t.Run("a sandbox key against a live route is a 401 or a 404, never data", func(t *testing.T) {
		// Ids of the other environment are simply not there.
		if r := call(t, "GET", "/v1/customers/cus_live", sandboxKey, nil); r.status != 404 {
			t.Errorf("sandbox key reads a live customer: %d %v", r.status, r.body)
		}
		if r := call(t, "GET", "/v1/customers/cus_sbx", liveKey, nil); r.status != 404 {
			t.Errorf("live key reads a sandbox customer: %d %v", r.status, r.body)
		}
		// A key whose prefix says the other environment is a hard 401, not a lookup miss.
		forgedLive := "qk_live_" + strings.TrimPrefix(sandboxKey, "qk_test_")
		forgedTest := "qk_test_" + strings.TrimPrefix(liveKey, "qk_live_")
		for _, key := range []string{forgedLive, forgedTest} {
			if r := call(t, "GET", "/v1/customers/cus_sbx", key, nil); r.status != 401 {
				t.Errorf("a key with the wrong environment in its prefix: %d %v", r.status, r.body)
			}
		}
		// The sandbox's tools are not on the live side.
		if r := call(t, "GET", "/v1/sandbox/scenarios", liveKey, nil); r.status != 403 {
			t.Errorf("sandbox scenarios with a live key: %d %v", r.status, r.body)
		}
	})

	// Configuration made in the sandbox.
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "e2e_api", "aggregation": "AGGREGATION_SUM", "value_property": "n", "quantity_scale": 0, "unit_label": "calls"}},
		{"/v1/plans", map[string]any{"code": "e2e_plan", "name": "E2E plan"}},
		{"/v1/plans/e2e_plan/versions", map[string]any{"base_fee_minor": 1000, "prices": []any{map[string]any{"meter_code": "e2e_api", "per_unit": map[string]any{"unit_price": "1"}}}}},
	} {
		if r := call(t, "POST", step.path, sandboxKey, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}

	t.Run("promote: the dry run shows the diff, applying adds it, and again changes nothing", func(t *testing.T) {
		dry := call(t, "POST", base+"/promote", session, map[string]any{"dry_run": true})
		if dry.status != 200 || dry.body["dry_run"] != true {
			t.Fatalf("dry run: %d %v", dry.status, dry.body)
		}
		hash, _ := dry.body["plan_hash"].(string)
		if hash == "" || num(dry.body["created"]) < 2 {
			t.Fatalf("the dry run shows no diff: %v", dry.body)
		}
		var sawMeter bool
		for _, raw := range dry.body["changes"].([]any) {
			c := raw.(map[string]any)
			if c["family"] == "meters" && c["key"] == "e2e_api" && c["action"] == "ACTION_CREATE" && c["applied"] != true {
				sawMeter = true
			}
		}
		if !sawMeter {
			t.Errorf("the meter is not in the diff: %v", dry.body["changes"])
		}
		if r := call(t, "GET", "/v1/meters/e2e_api", liveKey, nil); r.status != 404 {
			t.Fatalf("a dry run wrote to live: %d", r.status)
		}
		applied := call(t, "POST", base+"/promote", session, map[string]any{"dry_run": false, "plan_hash": hash})
		if applied.status != 200 || num(applied.body["created"]) < 2 {
			t.Fatalf("apply: %d %v", applied.status, applied.body)
		}
		if r := call(t, "GET", "/v1/meters/e2e_api", liveKey, nil); r.status != 200 {
			t.Fatalf("the meter did not reach live: %d %v", r.status, r.body)
		}
		again := call(t, "POST", base+"/promote", session, map[string]any{"dry_run": true})
		if again.status != 200 || num(again.body["created"]) != 0 || num(again.body["conflicts"]) != 0 {
			t.Fatalf("a second preview still has something to add: %v", again.body)
		}
		// A customer is never copied.
		if r := call(t, "GET", "/v1/customers/cus_sbx", liveKey, nil); r.status != 404 {
			t.Errorf("a sandbox customer reached live: %d", r.status)
		}
	})

	t.Run("reset removes the sandbox's customers and keeps its configuration, and live is untouched", func(t *testing.T) {
		if r := call(t, "POST", base+"/sandbox/reset", session, nil); r.status != 202 {
			t.Fatalf("reset: %d %v", r.status, r.body)
		}
		var state string
		for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
			s := call(t, "GET", base+"/sandbox/reset", session, nil)
			job, _ := s.body["sandbox_reset"].(map[string]any)
			state, _ = job["state"].(string)
			if state == "done" || state == "failed" {
				if state == "failed" {
					t.Fatalf("the reset failed: %v", job)
				}
				break
			}
		}
		if state != "done" {
			t.Fatalf("the reset did not finish: state %q", state)
		}
		if r := call(t, "GET", "/v1/customers/cus_sbx", sandboxKey, nil); r.status != 404 {
			t.Errorf("the sandbox customer survived the reset: %d", r.status)
		}
		if r := call(t, "GET", "/v1/meters/e2e_api", sandboxKey, nil); r.status != 200 {
			t.Errorf("the sandbox meter did not survive the reset: %d %v", r.status, r.body)
		}
		if r := call(t, "GET", "/v1/customers/cus_live", liveKey, nil); r.status != 200 {
			t.Errorf("the reset touched live: %d %v", r.status, r.body)
		}
		// Asking again at once is refused (a few minutes apart, per person).
		if r := call(t, "POST", base+"/sandbox/reset", session, nil); r.status != 429 || r.errKey() != "identity.sandbox.reset_rate_limited" {
			t.Errorf("a second reset at once: %d %v", r.status, r.body)
		}
	})
}

// num reads a JSON number that the gateway may write as a number or a string.
func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n := 0
		_, _ = fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}
