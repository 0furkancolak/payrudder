package apikey

import (
	"net/http"
	"slices"
	"strings"
)

// Permissions are what a key may do, named resource:action. plans-svc validates and stores them, the gateway enforces them on every tenant route
// (RequiredPermission), and the console offers them. A key has a role (a named set) and the permissions it was issued with: the role "custom" is any set.
const (
	PermEventsWrite        = "events:write"
	PermCustomersRead      = "customers:read"
	PermCustomersWrite     = "customers:write"
	PermSubscriptionsRead  = "subscriptions:read"
	PermSubscriptionsWrite = "subscriptions:write"
	PermCatalogRead        = "catalog:read"
	PermCatalogWrite       = "catalog:write"
	PermUsageRead          = "usage:read"
	PermCreditsRead        = "credits:read"
	PermCreditsWrite       = "credits:write"
	PermInvoicesRead       = "invoices:read"
	PermInvoicesWrite      = "invoices:write"
	PermReportsRead        = "reports:read"
	PermAccountingRead     = "accounting:read"
	PermAccountingWrite    = "accounting:write"
	PermWebhooksRead       = "webhooks:read"
	PermWebhooksWrite      = "webhooks:write"
	PermAffiliatesRead     = "affiliates:read"
	PermAffiliatesWrite    = "affiliates:write"
	PermSettingsRead       = "settings:read"
	PermSettingsWrite      = "settings:write"
	PermAssetsRead         = "assets:read"
	PermAssetsWrite        = "assets:write"
	PermSandboxWrite       = "sandbox:write"
)

// Permissions lists every permission, in the order the console shows them.
var Permissions = []string{
	PermEventsWrite,
	PermCustomersRead, PermCustomersWrite,
	PermSubscriptionsRead, PermSubscriptionsWrite,
	PermCatalogRead, PermCatalogWrite,
	PermUsageRead,
	PermCreditsRead, PermCreditsWrite,
	PermInvoicesRead, PermInvoicesWrite,
	PermReportsRead,
	PermAccountingRead, PermAccountingWrite,
	PermWebhooksRead, PermWebhooksWrite,
	PermAffiliatesRead, PermAffiliatesWrite,
	PermSettingsRead, PermSettingsWrite,
	PermAssetsRead, PermAssetsWrite,
	PermSandboxWrite,
}

// Key roles. A role other than custom is a fixed set of permissions.
const (
	RoleAdmin      = "admin"       // everything
	RoleReadOnly   = "read_only"   // every :read
	RoleIngestOnly = "ingest_only" // events:write only
	RoleBilling    = "billing"     // customers, subscriptions, credits and invoices, the catalogue and usage to read
	RoleCustom     = "custom"      // the permissions chosen
)

// Roles lists the roles in the order the console shows them.
var Roles = []string{RoleAdmin, RoleBilling, RoleReadOnly, RoleIngestOnly, RoleCustom}

// ValidPermission reports whether p is a known permission.
func ValidPermission(p string) bool { return slices.Contains(Permissions, p) }

// RolePermissions is the fixed set of a role; ok is false for an unknown role and for custom (which has no fixed set).
func RolePermissions(role string) (perms []string, ok bool) {
	switch role {
	case RoleAdmin:
		return slices.Clone(Permissions), true
	case RoleReadOnly:
		for _, p := range Permissions {
			if strings.HasSuffix(p, ":read") {
				perms = append(perms, p)
			}
		}
		return perms, true
	case RoleIngestOnly:
		return []string{PermEventsWrite}, true
	case RoleBilling:
		return []string{
			PermEventsWrite, PermCustomersRead, PermCustomersWrite, PermSubscriptionsRead, PermSubscriptionsWrite, PermCatalogRead, PermUsageRead,
			PermCreditsRead, PermCreditsWrite, PermInvoicesRead, PermInvoicesWrite,
		}, true
	}
	return nil, false
}

// Resolve checks a role and the chosen permissions and returns the role and the permission set to store. A fixed role takes its own set (permissions given
// with it must be inside that set; ok is false otherwise). The role custom needs at least one permission, all known. An empty role is admin when no
// permissions are chosen and custom when some are.
func Resolve(role string, chosen []string) (outRole string, perms []string, ok bool) {
	if role == "" {
		if len(chosen) == 0 {
			role = RoleAdmin
		} else {
			role = RoleCustom
		}
	}
	chosen = normalize(chosen)
	if role == RoleCustom {
		if len(chosen) == 0 {
			return "", nil, false
		}
		for _, p := range chosen {
			if !ValidPermission(p) {
				return "", nil, false
			}
		}
		return role, chosen, true
	}
	fixed, known := RolePermissions(role)
	if !known {
		return "", nil, false
	}
	for _, p := range chosen {
		if !slices.Contains(fixed, p) {
			return "", nil, false
		}
	}
	return role, fixed, true
}

func normalize(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	// The vocabulary's order, unknown names last: the same set always reads the same.
	slices.SortStableFunc(out, func(a, b string) int { return slices.Index(Permissions, a) - slices.Index(Permissions, b) })
	return out
}

// Allows reports whether the permission set contains want.
func Allows(perms []string, want string) bool { return slices.Contains(perms, want) }

// LegacyScope is the role the old scope names stand for ("full", "ingest", "read"); ok is false for any other name.
func LegacyScope(scope string) (role string, ok bool) {
	switch scope {
	case "", "full":
		return RoleAdmin, true
	case "ingest":
		return RoleIngestOnly, true
	case "read":
		return RoleReadOnly, true
	}
	return "", false
}

// RequiredPermission is the permission a tenant-API route needs: by the first path segments below /v1 and the method. ok is false for a path that no
// permission covers, so the gateway refuses it for every key that is not admin and a test over the route table fails when a new route is not covered.
func RequiredPermission(method, path string) (perm string, ok bool) {
	rest, found := strings.CutPrefix(path, "/v1/")
	if !found {
		return "", false
	}
	segs := strings.Split(rest, "/")
	read := method == http.MethodGet || method == http.MethodHead
	rw := func(resource string) (string, bool) {
		if read {
			return resource + ":read", true
		}
		return resource + ":write", true
	}
	switch segs[0] {
	case "events":
		return PermEventsWrite, true
	case "customers":
		if len(segs) >= 3 {
			switch segs[2] {
			case "credits":
				return rw("credits")
			case "usage", "entitlements":
				return PermUsageRead, true
			case "payments":
				return rw("invoices")
			case "coupons":
				return rw("subscriptions")
			case "referral":
				return rw("affiliates")
			case "portal-links":
				return rw("invoices")
			}
		}
		return rw("customers")
	case "subscriptions":
		return rw("subscriptions")
	case "promotions":
		return PermCatalogRead, true // a quote only reads
	case "plans", "coupons", "coupon-redemptions", "promotion-codes", "credit-packs", "meters":
		return rw("catalog")
	case "invoices", "credit-notes", "e-invoices", "payment-links", "payments":
		return rw("invoices")
	case "reports", "analytics":
		return PermReportsRead, true
	case "accounting", "expenses", "expense-inbox", "expense-rules":
		return rw("accounting")
	case "webhook-endpoints", "webhook-deliveries":
		return rw("webhooks")
	case "affiliates":
		return rw("affiliates")
	case "dunning-settings", "mail-settings", "routing-rules", "tax-rates", "tenant", "credits":
		return rw("settings")
	case "assets":
		return rw("assets")
	case "sandbox":
		return PermSandboxWrite, true // the scenario list is part of the sandbox tools
	}
	return "", false
}
