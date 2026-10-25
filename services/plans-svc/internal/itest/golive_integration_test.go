//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// envOf picks the tenant of an app in an environment.
func envOf(app *plansv1.App, e plansv1.Environment) *plansv1.Tenant {
	for _, tn := range app.GetTenants() {
		if tn.GetEnvironment() == e {
			return tn
		}
	}
	return nil
}

func itemDone(g *plansv1.GoLive, key string) bool {
	for _, it := range g.GetItems() {
		if it.GetKey() == key {
			return it.GetDone()
		}
	}
	return false
}

// testGoLive covers the gate: a live tenant is created inactive with no keys and no webhook endpoints, the checklist is computed from the data, only the
// owner activates and only a complete list passes, an operator can suspend (and the keys stop at once), and the database itself refuses what the code should
// never write.
func testGoLive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.verified(t, "golive-owner@example.com", "Golive Co")
	// A second person, a viewer and an admin, who may read the list but not activate.
	admin := e.signUp(t, "golive-admin@example.com", "Admin Co")
	viewer := e.signUp(t, "golive-viewer@example.com", "Viewer Co")

	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: owner.id, AccountId: owner.account, Name: "Shop", Currency: "TRY", Timezone: "Europe/Istanbul"})
	if err != nil {
		t.Fatal(err)
	}
	app := created.GetApp()
	live, sandbox := envOf(app, plansv1.Environment_ENVIRONMENT_LIVE), envOf(app, plansv1.Environment_ENVIRONMENT_SANDBOX)
	if live.GetStatus() != plansv1.TenantStatus_TENANT_STATUS_INACTIVE || sandbox.GetStatus() != plansv1.TenantStatus_TENANT_STATUS_ACTIVE {
		t.Fatalf("a new app: live %v, sandbox %v; want inactive and active", live.GetStatus(), sandbox.GetStatus())
	}
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'admin'), ($1::uuid, $3::uuid, 'viewer')`,
		owner.account, admin.GetUser().GetId(), viewer.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}

	t.Run("an inactive live tenant has no keys and no webhook endpoints", func(t *testing.T) {
		_, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
		wantCode(t, "member key", err, codes.FailedPrecondition, "plans.api_key.tenant_not_active")
		_, err = e.admin.CreateApiKey(operator(), &plansv1.CreateApiKeyRequest{TenantId: live.GetId()})
		wantCode(t, "operator key", err, codes.FailedPrecondition, "plans.api_key.tenant_not_active")
		_, err = e.hooks.CreateWebhookEndpoint(as(live.GetId()), &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/hook"})
		wantCode(t, "live webhook", err, codes.FailedPrecondition, "plans.webhook.tenant_not_active")
		if _, err := e.hooks.CreateWebhookEndpoint(as(sandbox.GetId()), &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/hook"}); err != nil {
			t.Errorf("the sandbox takes webhook endpoints at once: %v", err)
		}
		if _, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX}); err != nil {
			t.Errorf("the sandbox takes keys at once: %v", err)
		}
	})

	t.Run("the database refuses what the code should never write", func(t *testing.T) {
		hash := make([]byte, 32)
		// A live key for an inactive tenant, a test key for a live one, a live key for the sandbox: three ways to confuse the environments.
		for name, args := range map[string][]any{
			"live key, inactive tenant": {live.GetId(), "AAAAAAA1", "live"},
			"test key, live tenant":     {live.GetId(), "AAAAAAA2", "test"},
			"live key, sandbox tenant":  {sandbox.GetId(), "AAAAAAA3", "live"},
		} {
			_, err := e.system.Exec(ctx, `INSERT INTO api_keys (id, tenant_id, prefix, key_hash, env, status) VALUES (gen_random_uuid(), $1::uuid, $2, $4, $3, 'active')`,
				args[0], args[1], args[2], hash)
			if err == nil {
				t.Errorf("%s: the insert was accepted", name)
			}
		}
		if _, err := e.system.Exec(ctx, `UPDATE tenants SET status = 'inactive' WHERE id = $1::uuid`, sandbox.GetId()); err == nil {
			t.Error("a sandbox tenant was made inactive")
		}
	})

	t.Run("the checklist is computed from the data", func(t *testing.T) {
		g, err := e.ident.GetMyGoLive(ctx, &plansv1.GetMyGoLiveRequest{UserId: owner.id, AppId: app.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		list := g.GetGoLive()
		if list.GetReady() || list.GetStatus() != plansv1.TenantStatus_TENANT_STATUS_INACTIVE || len(list.GetItems()) != 5 || list.GetTenantId() != live.GetId() {
			t.Fatalf("go-live = %v", list)
		}
		// The owner verified the address; nothing else is done.
		if !itemDone(list, "email_verified") || itemDone(list, "legal_profile") || itemDone(list, "payment_provider") || itemDone(list, "plan_published") || itemDone(list, "terms_accepted") {
			t.Fatalf("items = %v", list.GetItems())
		}
		// A viewer reads it too; a stranger finds no such app.
		if _, err := e.ident.GetMyGoLive(ctx, &plansv1.GetMyGoLiveRequest{UserId: viewer.GetUser().GetId(), AppId: app.GetId()}); err != nil {
			t.Errorf("a member reads the checklist: %v", err)
		}
		other := e.verified(t, "golive-stranger@example.com", "Stranger Co")
		_, err = e.ident.GetMyGoLive(ctx, &plansv1.GetMyGoLiveRequest{UserId: other.id, AppId: app.GetId()})
		wantCode(t, "a stranger", err, codes.NotFound, "identity.app.not_found")
	})

	t.Run("only an owner activates, and only a complete list passes", func(t *testing.T) {
		for who, uid := range map[string]string{"admin": admin.GetUser().GetId(), "viewer": viewer.GetUser().GetId()} {
			_, err := e.ident.ActivateMyApp(ctx, &plansv1.ActivateMyAppRequest{UserId: uid, AppId: app.GetId(), ProviderReady: true, AcceptTerms: true})
			wantCode(t, who+" activates", err, codes.PermissionDenied, "identity.go_live.owner_only")
		}
		_, err := e.ident.ActivateMyApp(ctx, &plansv1.ActivateMyAppRequest{UserId: owner.id, AppId: app.GetId()})
		wantCode(t, "everything missing", err, codes.FailedPrecondition, "identity.go_live.incomplete")
		if err != nil && !strings.Contains(err.Error(), "legal_profile") {
			t.Errorf("the refusal does not name what is missing: %v", err)
		}

		// Legal profile: a legal name, a country and, in Turkey, a tax id.
		set := func(addr *plansv1.Address, legal *plansv1.LegalInfo) {
			t.Helper()
			if _, err := e.admin.UpdateTenantSettings(operator(), &plansv1.UpdateTenantSettingsRequest{TenantId: live.GetId(), Address: addr, Legal: legal}); err != nil {
				t.Fatal(err)
			}
		}
		set(&plansv1.Address{Line1: "Bagdat Cd. 1", City: "Istanbul", Country: "TR"}, &plansv1.LegalInfo{LegalName: "Shop AS"})
		if g, _ := e.ident.GetMyGoLive(ctx, &plansv1.GetMyGoLiveRequest{UserId: owner.id, AppId: app.GetId()}); itemDone(g.GetGoLive(), "legal_profile") {
			t.Error("a Turkish seller without a tax id passed the legal profile")
		}
		set(&plansv1.Address{Line1: "Bagdat Cd. 1", City: "Istanbul", Country: "TR"}, &plansv1.LegalInfo{LegalName: "Shop AS", TaxId: "1234567890"})
		// A published plan.
		if _, err := e.plans.CreatePlan(as(live.GetId()), &plansv1.CreatePlanRequest{Code: "pro", Name: "Pro"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(as(live.GetId()), &plansv1.PublishPlanVersionRequest{PlanCode: "pro", BaseFeeMinor: 9900}); err != nil {
			t.Fatal(err)
		}

		// Without the provider (payments-worker said no) or the terms, still no.
		_, err = e.ident.ActivateMyApp(ctx, &plansv1.ActivateMyAppRequest{UserId: owner.id, AppId: app.GetId(), AcceptTerms: true})
		wantCode(t, "no provider", err, codes.FailedPrecondition, "identity.go_live.incomplete")
		_, err = e.ident.ActivateMyApp(ctx, &plansv1.ActivateMyAppRequest{UserId: owner.id, AppId: app.GetId(), ProviderReady: true})
		wantCode(t, "terms not accepted", err, codes.FailedPrecondition, "identity.go_live.incomplete")
		g, err := e.ident.GetMyGoLive(ctx, &plansv1.GetMyGoLiveRequest{UserId: owner.id, AppId: app.GetId(), ProviderReady: true})
		if err != nil || g.GetGoLive().GetReady() || itemDone(g.GetGoLive(), "terms_accepted") {
			t.Fatalf("a refused activation must not accept the terms behind the owner's back: %v, %v", g, err)
		}

		done, err := e.ident.ActivateMyApp(ctx, &plansv1.ActivateMyAppRequest{UserId: owner.id, AppId: app.GetId(), ProviderReady: true, AcceptTerms: true})
		if err != nil {
			t.Fatalf("complete list: %v", err)
		}
		if done.GetGoLive().GetStatus() != plansv1.TenantStatus_TENANT_STATUS_ACTIVE || !done.GetGoLive().GetReady() {
			t.Fatalf("activated = %v", done)
		}
		again, err := e.ident.ActivateMyApp(ctx, &plansv1.ActivateMyAppRequest{UserId: owner.id, AppId: app.GetId(), ProviderReady: true})
		if err != nil || again.GetGoLive().GetStatus() != plansv1.TenantStatus_TENANT_STATUS_ACTIVE {
			t.Errorf("activating twice: %v, %v", again, err)
		}
		got, err := e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: owner.id, AppId: app.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		lt := envOf(got.GetApp(), plansv1.Environment_ENVIRONMENT_LIVE)
		if lt.GetStatus() != plansv1.TenantStatus_TENANT_STATUS_ACTIVE || lt.GetActivateTime() == nil || lt.GetTermsAcceptTime() == nil {
			t.Errorf("live tenant after activation = %v", lt)
		}
	})

	t.Run("an active live tenant takes keys and webhooks", func(t *testing.T) {
		k, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
		if err != nil || !strings.HasPrefix(k.GetPlaintext(), "qk_live_") {
			t.Fatalf("live key = %v, %v", k, err)
		}
		if _, err := e.hooks.CreateWebhookEndpoint(as(live.GetId()), &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/hook"}); err != nil {
			t.Errorf("live webhook: %v", err)
		}
		// An operator suspends: every key of the tenant leaves the gateway cache and resolves as suspended.
		e.evicted.mu.Lock()
		e.evicted.prefixes = nil
		e.evicted.mu.Unlock()
		sus, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: live.GetId(), Status: plansv1.TenantStatus_TENANT_STATUS_SUSPENDED, Reason: "chargebacks"})
		if err != nil || sus.GetTenant().GetStatus() != plansv1.TenantStatus_TENANT_STATUS_SUSPENDED {
			t.Fatalf("suspend = %v, %v", sus, err)
		}
		e.evicted.mu.Lock()
		evicted := append([]string(nil), e.evicted.prefixes...)
		e.evicted.mu.Unlock()
		if len(evicted) != 1 || evicted[0] != k.GetApiKey().GetPrefix() {
			t.Errorf("evicted = %v, want the one live key", evicted)
		}
		r, err := e.admin.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: k.GetApiKey().GetPrefix()})
		if err != nil || r.GetTenantStatus() != plansv1.TenantStatus_TENANT_STATUS_SUSPENDED {
			t.Fatalf("resolve = %v, %v", r, err)
		}
		_, err = e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
		wantCode(t, "key for a suspended tenant", err, codes.FailedPrecondition, "plans.api_key.tenant_not_active")
		_, err = e.ident.ActivateMyApp(ctx, &plansv1.ActivateMyAppRequest{UserId: owner.id, AppId: app.GetId(), ProviderReady: true, AcceptTerms: true})
		wantCode(t, "the owner reactivates a suspended tenant", err, codes.FailedPrecondition, "identity.go_live.suspended")
		if _, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: live.GetId(), Status: plansv1.TenantStatus_TENANT_STATUS_ACTIVE}); err != nil {
			t.Errorf("an operator reinstates a suspended tenant: %v", err)
		}
	})

	t.Run("the operator's hand has rules", func(t *testing.T) {
		acct := e.inactiveAccount(t, "Operator Co")
		inactive, sbx := acct[plansv1.Environment_ENVIRONMENT_LIVE], acct[plansv1.Environment_ENVIRONMENT_SANDBOX]
		_, err := e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: inactive.GetId(), Status: plansv1.TenantStatus_TENANT_STATUS_ACTIVE})
		wantCode(t, "activate without a reason", err, codes.InvalidArgument, "plans.tenant.reason_required")
		_, err = e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: inactive.GetId(), Status: plansv1.TenantStatus_TENANT_STATUS_SUSPENDED, Reason: "x"})
		wantCode(t, "suspend an inactive tenant", err, codes.FailedPrecondition, "plans.tenant.bad_transition")
		_, err = e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: sbx.GetId(), Status: plansv1.TenantStatus_TENANT_STATUS_SUSPENDED, Reason: "x"})
		wantCode(t, "suspend a sandbox", err, codes.FailedPrecondition, "plans.tenant.bad_transition")
		_, err = e.admin.SetTenantStatus(operator(), &plansv1.SetTenantStatusRequest{TenantId: inactive.GetId(), Status: plansv1.TenantStatus_TENANT_STATUS_INACTIVE, Reason: "x"})
		wantCode(t, "make a tenant inactive", err, codes.InvalidArgument, "plans.tenant.bad_status")
		_, err = e.admin.SetTenantStatus(nil2(), &plansv1.SetTenantStatusRequest{TenantId: inactive.GetId(), Status: plansv1.TenantStatus_TENANT_STATUS_ACTIVE, Reason: "x"})
		if c := code(t, err); c != codes.PermissionDenied && c != codes.Unauthenticated {
			t.Errorf("without the operator: %v", err)
		}
	})
}

// testAPIKeyScopesAndRotation covers the restricted keys, last used and the rotation overlap.
func testAPIKeyScopesAndRotation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.verified(t, "keys-owner@example.com", "Keys Co")
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: p.id, AccountId: p.account, Name: "Keys", Currency: "USD", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	app := created.GetApp()
	mk := func(scope plansv1.ApiKey_Scope) *plansv1.CreateMyApiKeyResponse {
		t.Helper()
		k, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: p.id, AppId: app.GetId(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX, Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	full, ingest, read := mk(plansv1.ApiKey_SCOPE_UNSPECIFIED), mk(plansv1.ApiKey_SCOPE_INGEST), mk(plansv1.ApiKey_SCOPE_READ)
	for key, want := range map[*plansv1.CreateMyApiKeyResponse]plansv1.ApiKey_Scope{full: plansv1.ApiKey_SCOPE_FULL, ingest: plansv1.ApiKey_SCOPE_INGEST, read: plansv1.ApiKey_SCOPE_READ} {
		r, err := e.admin.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: key.GetApiKey().GetPrefix()})
		if err != nil || r.GetScope() != want || key.GetApiKey().GetScope() != want {
			t.Errorf("scope of %s = %v / %v, want %v (%v)", key.GetApiKey().GetPrefix(), key.GetApiKey().GetScope(), r.GetScope(), want, err)
		}
	}
	_, err = e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: p.id, AppId: app.GetId(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX, Scope: plansv1.ApiKey_Scope(99)})
	wantCode(t, "unknown scope", err, codes.InvalidArgument, "identity.api_key.bad_scope")

	// last_used: written, and not moved by a second use within the minute.
	if got := lastUsed(t, e, ingest.GetApiKey().GetId()); got != nil {
		t.Fatalf("a new key was used: %v", got)
	}
	if _, err := e.admin.TouchApiKey(ctx, &plansv1.TouchApiKeyRequest{ApiKeyId: ingest.GetApiKey().GetId()}); err != nil {
		t.Fatal(err)
	}
	first := lastUsed(t, e, ingest.GetApiKey().GetId())
	if first == nil {
		t.Fatal("last_used_time was not written")
	}
	if _, err := e.admin.TouchApiKey(ctx, &plansv1.TouchApiKeyRequest{ApiKeyId: ingest.GetApiKey().GetId()}); err != nil {
		t.Fatal(err)
	}
	if second := lastUsed(t, e, ingest.GetApiKey().GetId()); second == nil || !second.Equal(*first) {
		t.Errorf("a second touch within the minute moved the time: %v -> %v", first, second)
	}
	if _, err := e.admin.TouchApiKey(ctx, &plansv1.TouchApiKeyRequest{ApiKeyId: "nope"}); code(t, err) != codes.InvalidArgument {
		t.Errorf("a bad id: %v", err)
	}

	// Rotation: a replacement of the same scope, the old key ends after the overlap, and a key can be rotated once.
	rot, err := e.ident.RotateMyApiKey(ctx, &plansv1.RotateMyApiKeyRequest{UserId: p.id, AppId: app.GetId(), ApiKeyId: ingest.GetApiKey().GetId(), Overlap: durationpb.New(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if rot.GetApiKey().GetScope() != plansv1.ApiKey_SCOPE_INGEST || !strings.HasPrefix(rot.GetPlaintext(), "qk_test_") || rot.GetApiKey().GetId() == ingest.GetApiKey().GetId() {
		t.Fatalf("replacement = %v", rot)
	}
	exp := rot.GetPrevious().GetExpireTime().AsTime()
	if d := time.Until(exp); d < 110*time.Minute || d > 121*time.Minute || rot.GetPrevious().GetStatus() != plansv1.ApiKey_STATUS_ACTIVE {
		t.Errorf("old key ends in %v (status %v), want about 2h and still active", d, rot.GetPrevious().GetStatus())
	}
	if r, _ := e.admin.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: ingest.GetApiKey().GetPrefix()}); r.GetExpireTime() == nil {
		t.Error("the gateway is not told when the old key ends")
	}
	_, err = e.ident.RotateMyApiKey(ctx, &plansv1.RotateMyApiKeyRequest{UserId: p.id, AppId: app.GetId(), ApiKeyId: ingest.GetApiKey().GetId()})
	wantCode(t, "rotate twice", err, codes.FailedPrecondition, "identity.api_key.already_rotating")
	_, err = e.ident.RotateMyApiKey(ctx, &plansv1.RotateMyApiKeyRequest{UserId: p.id, AppId: app.GetId(), ApiKeyId: full.GetApiKey().GetId(), Overlap: durationpb.New(8 * 24 * time.Hour)})
	wantCode(t, "overlap too long", err, codes.InvalidArgument, "identity.api_key.bad_overlap")
	if _, err := e.ident.RevokeMyApiKey(ctx, &plansv1.RevokeMyApiKeyRequest{UserId: p.id, AppId: app.GetId(), ApiKeyId: read.GetApiKey().GetId()}); err != nil {
		t.Fatal(err)
	}
	_, err = e.ident.RotateMyApiKey(ctx, &plansv1.RotateMyApiKeyRequest{UserId: p.id, AppId: app.GetId(), ApiKeyId: read.GetApiKey().GetId()})
	wantCode(t, "rotate a revoked key", err, codes.FailedPrecondition, "identity.api_key.not_active")
	_, err = e.ident.RotateMyApiKey(ctx, &plansv1.RotateMyApiKeyRequest{UserId: p.id, AppId: app.GetId(), ApiKeyId: "00000000-0000-4000-8000-000000000000"})
	wantCode(t, "rotate an unknown key", err, codes.NotFound, "identity.api_key.not_found")
	// The default overlap is a day.
	def, err := e.ident.RotateMyApiKey(ctx, &plansv1.RotateMyApiKeyRequest{UserId: p.id, AppId: app.GetId(), ApiKeyId: full.GetApiKey().GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(def.GetPrevious().GetExpireTime().AsTime()); d < 23*time.Hour || d > 25*time.Hour {
		t.Errorf("default overlap = %v", d)
	}
}

func lastUsed(t *testing.T, e *env, keyID string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := e.system.QueryRow(context.Background(), `SELECT last_used_at FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}
