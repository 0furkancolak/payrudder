//go:build integration

package itest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
)

// testAppRenameAndDelete covers renaming and deleting an app: the roles that may, the names of the tenants, and what "deleted" means (archived: gone from
// every list and route of the account, keys revoked and refused for good, the billing records kept).
func testAppRenameAndDelete(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.verified(t, "ada.apps@example.com", "Ada Apps")
	bob := e.verified(t, "bob.apps@example.com", "Bob Apps")
	member := func(email, role string) person {
		p := e.verified(t, email, "Own "+email)
		if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, $3)`, ada.account, p.id, role); err != nil {
			t.Fatal(err)
		}
		return p
	}
	admin, viewer := member("admin.apps@example.com", "admin"), member("viewer.apps@example.com", "viewer")

	mk := func(name string) *plansv1.App {
		t.Helper()
		r, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: ada.id, AccountId: ada.account, Name: name, Currency: "TRY", Timezone: "Europe/Istanbul"})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetApp()
	}
	shop, other := mk("Web shop"), mk("Other")
	tenantOf := func(app *plansv1.App, env plansv1.Environment) *plansv1.Tenant {
		for _, tn := range app.GetTenants() {
			if tn.GetEnvironment() == env {
				return tn
			}
		}
		t.Fatal("no such tenant")
		return nil
	}
	live, sandbox := tenantOf(shop, plansv1.Environment_ENVIRONMENT_LIVE), tenantOf(shop, plansv1.Environment_ENVIRONMENT_SANDBOX)
	e.activate(t, live.GetId())
	rename := func(p person, app *plansv1.App, name string) (*plansv1.RenameMyAppResponse, error) {
		return e.ident.RenameMyApp(ctx, &plansv1.RenameMyAppRequest{UserId: p.id, AppId: app.GetId(), Name: name})
	}
	del := func(p person, app *plansv1.App) error {
		_, err := e.ident.DeleteMyApp(ctx, &plansv1.DeleteMyAppRequest{UserId: p.id, AppId: app.GetId()})
		return err
	}

	t.Run("Rename", func(t *testing.T) {
		r, err := rename(ada, shop, "  Web shop 2 ")
		if err != nil || r.GetApp().GetName() != "Web shop 2" || r.GetApp().GetSlug() != "web-shop" || len(r.GetApp().GetTenants()) != 2 {
			t.Fatalf("rename = %v, %v", r, err)
		}
		// The tenants carry the name too, the sandbox one with its suffix; the first tenant of an app is its sandbox.
		if got := r.GetApp().GetTenants(); got[0].GetName() != "Web shop 2 (sandbox)" || got[1].GetName() != "Web shop 2" {
			t.Errorf("tenant names = %q, %q", got[0].GetName(), got[1].GetName())
		}
		if res, err := e.ident.ResolveMyTenant(ctx, &plansv1.ResolveMyTenantRequest{UserId: ada.id, TenantId: live.GetId()}); err != nil || res.GetTenant().GetName() != "Web shop 2" {
			t.Errorf("live tenant = %v, %v", res, err)
		}
		// An admin may rename; a viewer and another account's owner may not, and see no app.
		if _, err := rename(admin, shop, "Web shop"); err != nil {
			t.Errorf("an admin renames: %v", err)
		}
		_, err = rename(viewer, shop, "Hijack")
		wantCode(t, "a viewer renames", err, codes.PermissionDenied, "identity.account.forbidden")
		_, err = rename(bob, shop, "Hijack")
		wantCode(t, "another account's owner renames", err, codes.NotFound, "identity.app.not_found")
		for _, bad := range []string{"", "   ", string(make([]byte, 201))} {
			_, err = rename(ada, shop, bad)
			wantCode(t, "a bad name", err, codes.InvalidArgument, "")
		}
		var detail string
		if err := e.system.QueryRow(ctx, `SELECT detail->>'from' || '>' || (detail->>'to') FROM app_audit WHERE app_id = $1::uuid AND action = 'renamed' ORDER BY created_at, id LIMIT 1`, shop.GetId()).Scan(&detail); err != nil || detail != "Web shop>Web shop 2" {
			t.Errorf("audit of the rename = %q, %v", detail, err)
		}
	})

	t.Run("DeleteRoles", func(t *testing.T) {
		// Only an owner ends an app: an admin manages it, a viewer reads it.
		wantCode(t, "an admin deletes", del(admin, other), codes.PermissionDenied, "identity.account.forbidden")
		wantCode(t, "a viewer deletes", del(viewer, other), codes.PermissionDenied, "identity.account.forbidden")
		wantCode(t, "another account's owner deletes", del(bob, other), codes.NotFound, "identity.app.not_found")
		if l, err := e.ident.ListMyApps(ctx, &plansv1.ListMyAppsRequest{UserId: ada.id, AccountId: ada.account}); err != nil || len(l.GetApps()) != 2 {
			t.Errorf("apps after the refused deletes = %v, %v", l, err)
		}
	})

	t.Run("DeleteArchivesAndKeepsTheBillingRecords", func(t *testing.T) {
		lk, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: ada.id, AppId: shop.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
		if err != nil {
			t.Fatal(err)
		}
		sk, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: ada.id, AppId: shop.GetId(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX})
		if err != nil {
			t.Fatal(err)
		}
		// Money data of the live environment: a customer, a plan, a subscription and the rest of what a tenant holds.
		seed(t, e, live.GetId(), "shop")
		count := func(table string) (n int) {
			t.Helper()
			if err := e.scan(ctx, live.GetId(), `SELECT count(*) FROM `+table+` WHERE tenant_id = $1::uuid`, []any{live.GetId()}, &n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		customers, subs := count("customers"), count("subscriptions")
		if customers == 0 || subs == 0 {
			t.Fatalf("setup: %d customers, %d subscriptions", customers, subs)
		}
		e.evicted.mu.Lock()
		e.evicted.prefixes = nil
		e.evicted.mu.Unlock()

		if err := del(ada, shop); err != nil {
			t.Fatal(err)
		}

		// Gone for the account: lists and every route of a person.
		l, err := e.ident.ListMyApps(ctx, &plansv1.ListMyAppsRequest{UserId: ada.id, AccountId: ada.account})
		if err != nil || len(l.GetApps()) != 1 || l.GetApps()[0].GetId() != other.GetId() {
			t.Errorf("apps after the delete = %v, %v", l, err)
		}
		_, err = e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: ada.id, AppId: shop.GetId()})
		wantCode(t, "get a deleted app", err, codes.NotFound, "identity.app.not_found")
		_, err = e.ident.ResolveMyTenant(ctx, &plansv1.ResolveMyTenantRequest{UserId: ada.id, TenantId: live.GetId()})
		wantCode(t, "resolve the tenant of a deleted app", err, codes.NotFound, "identity.tenant.not_found")
		_, err = e.ident.ListMyApiKeys(ctx, &plansv1.ListMyApiKeysRequest{UserId: ada.id, AppId: shop.GetId()})
		wantCode(t, "list the keys of a deleted app", err, codes.NotFound, "identity.app.not_found")
		_, err = rename(ada, shop, "Back")
		wantCode(t, "rename a deleted app", err, codes.NotFound, "identity.app.not_found")
		wantCode(t, "delete twice", del(ada, shop), codes.NotFound, "identity.app.not_found")

		// Ingest is refused: every key is revoked and was evicted from the gateway cache, and no key can be made again, by a member or the operator.
		for _, k := range []*plansv1.ApiKey{lk.GetApiKey(), sk.GetApiKey()} {
			r, err := e.admin.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: k.GetPrefix()})
			if err != nil || r.GetStatus() != plansv1.ApiKey_STATUS_REVOKED {
				t.Errorf("key %s after the delete = %v, %v", k.GetPrefix(), r, err)
			}
		}
		e.evicted.mu.Lock()
		evicted := map[string]bool{}
		for _, p := range e.evicted.prefixes {
			evicted[p] = true
		}
		e.evicted.mu.Unlock()
		if !evicted[lk.GetApiKey().GetPrefix()] || !evicted[sk.GetApiKey().GetPrefix()] {
			t.Errorf("evicted %v: both keys leave the gateway cache at once", evicted)
		}
		_, err = e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: ada.id, AppId: shop.GetId(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX})
		wantCode(t, "a key for a deleted app", err, codes.NotFound, "identity.app.not_found")
		for _, tn := range []*plansv1.Tenant{live, sandbox} {
			_, err = e.admin.CreateApiKey(operator(), &plansv1.CreateApiKeyRequest{TenantId: tn.GetId()})
			wantCode(t, "the operator makes a key for a deleted app", err, codes.FailedPrecondition, "plans.api_key.app_archived")
		}
		// And the database itself refuses an active key, whatever the code does.
		_, err = e.system.Exec(ctx, `UPDATE api_keys SET status = 'active' WHERE id = $1::uuid`, lk.GetApiKey().GetId())
		if pgxx.ErrCode(err) != pgxx.CodeRaiseException {
			t.Errorf("reviving a key of a deleted app: %v", err)
		}

		// Nothing was erased: the records of the money are where they were, and the operator still sees the app, archived.
		if count("customers") != customers || count("subscriptions") != subs {
			t.Errorf("billing records after the delete: %d customers, %d subscriptions (were %d, %d)", count("customers"), count("subscriptions"), customers, subs)
		}
		var tenants int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE app_id = $1::uuid`, shop.GetId()).Scan(&tenants); err != nil || tenants != 2 {
			t.Errorf("tenants of the deleted app = %d, %v", tenants, err)
		}
		ops, err := e.admin.ListApps(operator(), &plansv1.ListAppsRequest{AccountId: ada.account})
		if err != nil || len(ops.GetApps()) != 2 {
			t.Fatalf("the operator's apps = %v, %v", ops, err)
		}
		var archived *plansv1.App
		for _, a := range ops.GetApps() {
			if a.GetId() == shop.GetId() {
				archived = a
			}
		}
		if archived == nil || archived.GetArchiveTime() == nil || archived.GetName() != "Web shop" {
			t.Errorf("the operator's view of the deleted app = %v", archived)
		}
		var by, detail string
		if err := e.system.QueryRow(ctx, `SELECT user_id::text, detail::text FROM app_audit WHERE app_id = $1::uuid AND action = 'archived'`, shop.GetId()).Scan(&by, &detail); err != nil || by != ada.id || detail != `{"revoked_keys": 2}` {
			t.Errorf("audit of the delete = %q %q, %v", by, detail, err)
		}

		// The slug is free again: a new app may take the name of the deleted one.
		again := mk("Web shop")
		if again.GetSlug() != "web-shop" || again.GetId() == shop.GetId() {
			t.Errorf("a new app with the old name = %v", again)
		}
	})

	t.Run("DeletedAppsDoNotCountAgainstTheQuota", func(t *testing.T) {
		for i := 0; i < 25; i++ {
			a := mk("Churn " + string(rune('a'+i)))
			if err := del(ada, a); err != nil {
				t.Fatalf("delete %d: %v", i, err)
			}
		}
	})
}
