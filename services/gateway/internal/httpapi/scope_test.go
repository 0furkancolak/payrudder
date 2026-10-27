package httpapi

import (
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/auth"

	"github.com/0furkancolak/payrudder/pkg/apikey"
)

// A key holds permissions, and a route needs one: without it the gateway answers 403 with the error key gateway.auth.permission_denied, naming the
// permission that was missing.
func TestPermissionsOfAKeyLimitWhatItCanCall(t *testing.T) {
	ts, _ := newTestServer(t)
	body := `{"external_id":"c1","name":"Ada","email":"ada@example.com"}`
	for name, tc := range map[string]struct {
		key, method, path string
		denied            bool // 403 is the permission check; otherwise the request gets past it (the fake upstream answers whatever it answers)
		missing           string
	}{
		"read key lists":                       {"read-key", "GET", "/v1/customers", false, ""},
		"read key cannot create":               {"read-key", "POST", "/v1/customers", true, "customers:write"},
		"read key cannot send events":          {"read-key", "POST", "/v1/events", true, "events:write"},
		"read key cannot cancel":               {"read-key", "DELETE", "/v1/subscriptions/00000000-0000-4000-8000-000000000000", true, "subscriptions:write"},
		"ingest key cannot create":             {"ingest-key", "POST", "/v1/customers", true, "customers:write"},
		"ingest key cannot read":               {"ingest-key", "GET", "/v1/customers", true, "customers:read"},
		"ingest key cannot read invoices":      {"ingest-key", "GET", "/v1/invoices", true, "invoices:read"},
		"admin key creates":                    {"sandbox-key", "POST", "/v1/customers", false, ""},
		"billing key creates customers":        {"billing-key", "POST", "/v1/customers", false, ""},
		"billing key cannot edit the catalog":  {"billing-key", "POST", "/v1/plans", true, "catalog:write"},
		"billing key cannot touch webhooks":    {"billing-key", "GET", "/v1/webhook-endpoints", true, "webhooks:read"},
		"custom key reads customers":           {"custom-key", "GET", "/v1/customers", false, ""},
		"custom key reads invoices":            {"custom-key", "GET", "/v1/invoices", false, ""},
		"custom key cannot read subscriptions": {"custom-key", "GET", "/v1/subscriptions", true, "subscriptions:read"},
		"custom key cannot write customers":    {"custom-key", "POST", "/v1/customers", true, "customers:write"},
		"custom key cannot send events":        {"custom-key", "POST", "/v1/events", true, "events:write"},
	} {
		resp, m := do(t, ts, tc.method, tc.path, tc.key, body)
		if (resp.StatusCode == 403) != tc.denied {
			t.Errorf("%s: status %d (%v), want denied=%v", name, resp.StatusCode, m, tc.denied)
		}
		if tc.denied {
			if errKey(m) != "gateway.auth.permission_denied" {
				t.Errorf("%s: error key %q, want gateway.auth.permission_denied", name, errKey(m))
			}
			e, _ := m["error"].(map[string]any)
			if msg, _ := e["message"].(string); !strings.Contains(msg, tc.missing) {
				t.Errorf("%s: message %q does not name %s", name, msg, tc.missing)
			}
		}
	}
	// An ingest key does what it is for: the request gets past the check (an empty batch is the handler's own 400, not the gateway's 403).
	resp, m := do(t, ts, "POST", "/v1/events", "ingest-key", `{"events":[]}`)
	if resp.StatusCode == 403 {
		t.Errorf("ingest key refused on /v1/events: %v", m)
	}
}

// Every route an API key can reach needs a permission, and it is one that exists: a route added without being classified would be closed to restricted keys
// (and open to admin keys only), so the table is checked here instead of discovered by a customer.
func TestEveryTenantRouteNeedsAKnownPermission(t *testing.T) {
	n := 0
	for _, rt := range (&Server{}).routes() {
		if rt.Access != AccessTenant && rt.Access != AccessSandbox {
			continue
		}
		n++
		perm, ok := apikey.RequiredPermission(rt.Method, rt.Path)
		if !ok {
			t.Errorf("%s %s needs no known permission: classify it in pkg/apikey RequiredPermission", rt.Method, rt.Path)
			continue
		}
		if !apikey.ValidPermission(perm) {
			t.Errorf("%s %s needs %q, which is not in the vocabulary", rt.Method, rt.Path, perm)
		}
	}
	if n < 100 {
		t.Errorf("only %d tenant routes seen: the route table was not read", n)
	}
}

// The role admin is refused nothing, and a restricted key is refused a route nobody classified.
func TestPermittedFailsClosedForUnclassifiedRoutes(t *testing.T) {
	admin, _ := apikey.RolePermissions(apikey.RoleAdmin)
	if _, ok := permitted(keyOf(apikey.RoleAdmin, admin), "GET", "/v1/not-yet-classified"); !ok {
		t.Error("an admin key was refused an unclassified route")
	}
	ro, _ := apikey.RolePermissions(apikey.RoleReadOnly)
	if _, ok := permitted(keyOf(apikey.RoleReadOnly, ro), "GET", "/v1/not-yet-classified"); ok {
		t.Error("a read-only key was allowed an unclassified route")
	}
}

func keyOf(role string, perms []string) auth.Principal {
	return auth.Principal{Tenant: tenant.Info{}, Role: role, Permissions: perms}
}
