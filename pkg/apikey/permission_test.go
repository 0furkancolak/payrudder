package apikey

import (
	"net/http"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name   string
		role   string
		chosen []string
		want   string
		ok     bool
	}{
		{"default is admin", "", nil, RoleAdmin, true},
		{"chosen permissions make a custom key", "", []string{PermCustomersRead}, RoleCustom, true},
		{"a fixed role takes its set", RoleIngestOnly, nil, RoleIngestOnly, true},
		{"a fixed role may name a subset", RoleReadOnly, []string{PermCustomersRead}, RoleReadOnly, true},
		{"a fixed role refuses a permission outside it", RoleReadOnly, []string{PermCustomersWrite}, "", false},
		{"custom needs a permission", RoleCustom, nil, "", false},
		{"custom refuses an unknown permission", RoleCustom, []string{"nope:read"}, "", false},
		{"unknown role", "root", nil, "", false},
	}
	for _, c := range cases {
		role, perms, ok := Resolve(c.role, c.chosen)
		if ok != c.ok || role != c.want {
			t.Errorf("%s: got %q %v, want %q %v", c.name, role, ok, c.want, c.ok)
		}
		if ok && len(perms) == 0 {
			t.Errorf("%s: no permissions", c.name)
		}
	}
}

func TestRolePermissions(t *testing.T) {
	admin, _ := RolePermissions(RoleAdmin)
	if len(admin) != len(Permissions) {
		t.Fatalf("admin has %d of %d", len(admin), len(Permissions))
	}
	ro, _ := RolePermissions(RoleReadOnly)
	for _, p := range ro {
		if !strings.HasSuffix(p, ":read") {
			t.Errorf("read_only has %s", p)
		}
	}
	for _, role := range Roles {
		if role == RoleCustom {
			continue
		}
		perms, ok := RolePermissions(role)
		if !ok {
			t.Fatalf("role %s unknown", role)
		}
		for _, p := range perms {
			if !ValidPermission(p) {
				t.Errorf("role %s has unknown permission %s", role, p)
			}
		}
	}
}

func TestRequiredPermission(t *testing.T) {
	cases := []struct{ method, path, want string }{
		{http.MethodPost, "/v1/events", PermEventsWrite},
		{http.MethodGet, "/v1/customers", PermCustomersRead},
		{http.MethodPost, "/v1/customers", PermCustomersWrite},
		{http.MethodPost, "/v1/customers/{external_id}/credits/grant", PermCreditsWrite},
		{http.MethodGet, "/v1/customers/{external_id}/entitlements", PermUsageRead},
		{http.MethodGet, "/v1/customers/{external_id}/usage", PermUsageRead},
		{http.MethodPost, "/v1/subscriptions/{id}/change", PermSubscriptionsWrite},
		{http.MethodPost, "/v1/promotions/quote", PermCatalogRead},
		{http.MethodGet, "/v1/reports/revenue", PermReportsRead},
		{http.MethodPut, "/v1/tax-rates", PermSettingsWrite},
		{http.MethodPost, "/v1/webhook-endpoints", PermWebhooksWrite},
		{http.MethodPost, "/v1/sandbox/close-period", PermSandboxWrite},
	}
	for _, c := range cases {
		got, ok := RequiredPermission(c.method, c.path)
		if !ok || got != c.want {
			t.Errorf("%s %s: got %q %v, want %q", c.method, c.path, got, ok, c.want)
		}
		if ok && !ValidPermission(got) {
			t.Errorf("%s %s needs the unknown permission %s", c.method, c.path, got)
		}
	}
	if _, ok := RequiredPermission(http.MethodGet, "/admin/v1/tenants"); ok {
		t.Error("an admin path needs no key permission")
	}
	if _, ok := RequiredPermission(http.MethodGet, "/v1/unheard-of"); ok {
		t.Error("an unknown route must not be covered")
	}
}
