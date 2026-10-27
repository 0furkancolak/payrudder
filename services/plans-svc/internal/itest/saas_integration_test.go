//go:build integration

package itest

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	envpkg "github.com/0furkancolak/payrudder/pkg/env"
	saascmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/command"
)

func saasConfig() saascmd.Config {
	return saascmd.Config{Enabled: true, Environment: envpkg.Live, Currency: "USD", TrialDays: 14}
}

// platformOf returns the platform tenant the SaaS mode runs on.
func (e *env) platformOf(t *testing.T) *plansv1.PlatformTenant {
	t.Helper()
	r, err := e.saasAPI.GetPlatform(context.Background(), &plansv1.GetPlatformRequest{})
	if err != nil || !r.GetEnabled() || r.GetTenant().GetId() == "" {
		t.Fatalf("GetPlatform = %v, %v", r, err)
	}
	return r.GetTenant()
}

func (e *env) myPlan(t *testing.T, p person) *plansv1.OrganizationPlan {
	t.Helper()
	r, err := e.saasAPI.GetMyPlan(context.Background(), &plansv1.GetMyPlanRequest{UserId: p.id, AccountId: p.account})
	if err != nil {
		t.Fatalf("GetMyPlan: %v", err)
	}
	return r.GetPlan()
}

func quotaOf(p *plansv1.OrganizationPlan, resource string) *plansv1.PlanQuota {
	for _, q := range p.GetQuotas() {
		if q.GetResource() == resource {
			return q
		}
	}
	return nil
}

func (e *env) createApp(p person, name string) (*plansv1.CreateMyAppResponse, error) {
	return e.ident.CreateMyApp(context.Background(), &plansv1.CreateMyAppRequest{UserId: p.id, AccountId: p.account, Name: name, Currency: "USD", Timezone: "UTC"})
}

// testSaas covers phase 55: the platform tenant and its plans, every account as a customer on Free, the limits where apps, members and keys are created,
// moving between plans, the operator's view and override, the ingest policy, and that none of it touches another tenant.
func testSaas(t *testing.T) {
	e := newEnvWith(t, saasConfig())
	ctx := context.Background()

	t.Run("BootstrapIsIdempotentAndSeedsThePlans", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 6)
		for range 6 {
			wg.Go(func() { errs <- e.saas.Bootstrap(ctx) })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("Bootstrap: %v", err)
			}
		}
		var accounts, tenants int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE platform`).Scan(&accounts); err != nil || accounts != 1 {
			t.Fatalf("platform accounts = %d, %v; want exactly one", accounts, err)
		}
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM tenants t JOIN accounts a ON a.id = t.account_id WHERE a.platform`).Scan(&tenants); err != nil || tenants != 1 {
			t.Fatalf("platform tenants = %d, %v; want exactly one", tenants, err)
		}
		pt := e.platformOf(t)
		if pt.GetEnvironment() != "live" || pt.GetCurrency() != "USD" {
			t.Errorf("platform = %v", pt)
		}
		plans, err := e.plans.ListPlans(as(pt.GetId()), &plansv1.ListPlansRequest{})
		if err != nil || len(plans.GetPlans()) != 3 {
			t.Fatalf("platform plans = %v, %v", plans, err)
		}
		for _, p := range plans.GetPlans() {
			if p.GetLatestVersion() != 1 {
				t.Errorf("plan %s is on version %d after six bootstraps, want 1", p.GetCode(), p.GetLatestVersion())
			}
		}
		pro, err := e.plans.GetPlanVersion(as(pt.GetId()), &plansv1.GetPlanVersionRequest{PlanCode: "pro"})
		if err != nil || pro.GetPlanVersion().GetBaseFeeMinor() != 2900 || len(pro.GetPlanVersion().GetEntitlements()) != 4 {
			t.Errorf("pro = %v, %v", pro, err)
		}
		// An operator's edit is kept: a new version of Free is not overwritten by the next start.
		_, err = e.plans.PublishPlanVersion(as(pt.GetId()), &plansv1.PublishPlanVersionRequest{PlanCode: "free", BaseFeeMinor: 0,
			Prices: []*plansv1.Price{{MeterCode: "events", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "0"}}}}, Entitlements: []*plansv1.Entitlement{
				{MeterCode: "events", Limit: "10000"}, {MeterCode: "apps", Limit: "1"}, {MeterCode: "members", Limit: "2"}, {MeterCode: "api_keys", Limit: "4"}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.saas.Bootstrap(ctx); err != nil {
			t.Fatal(err)
		}
		got, _ := e.plans.ListPlans(as(pt.GetId()), &plansv1.ListPlansRequest{})
		for _, p := range got.GetPlans() {
			if p.GetCode() == "free" && p.GetLatestVersion() != 2 {
				t.Errorf("free is on version %d, want the operator's 2", p.GetLatestVersion())
			}
		}
	})

	ada := e.verified(t, "ada@saas.example", "Ada SaaS")
	ben := e.verified(t, "ben@saas.example", "Ben SaaS")

	t.Run("AnAccountIsACustomerOnFree", func(t *testing.T) {
		p := e.myPlan(t, ada)
		if !p.GetEnabled() || p.GetPlan().GetCode() != "free" || p.GetState() != "active" || !p.GetCanManage() {
			t.Fatalf("plan = %v", p)
		}
		if len(p.GetOffers()) != 3 || p.GetOffers()[0].GetCode() != "free" || p.GetOffers()[2].GetCode() != "scale" {
			t.Errorf("offers = %v", p.GetOffers())
		}
		if q := quotaOf(p, "apps"); q.GetLimit() != 1 || q.GetUsed() != 0 || q.GetMode() != "hard" {
			t.Errorf("apps quota = %v", q)
		}
		if q := quotaOf(p, "members"); q.GetLimit() != 2 || q.GetUsed() != 1 {
			t.Errorf("members quota = %v", q)
		}
		pt := e.platformOf(t)
		cust, err := e.plans.GetCustomer(as(pt.GetId()), &plansv1.GetCustomerRequest{ExternalId: ada.account})
		if err != nil || cust.GetCustomer().GetEmail() != "ada@saas.example" {
			t.Fatalf("the account is not a customer of the platform tenant: %v, %v", cust, err)
		}
		// Asking again, and many at once, still leaves one customer with one subscription.
		var wg sync.WaitGroup
		for range 6 {
			wg.Go(func() { _ = e.myPlanQuiet(ada) })
		}
		wg.Wait()
		var subs int
		if err := e.scan(ctx, e.platformOf(t).GetId(), `SELECT count(*) FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id WHERE c.external_id = $1`, []any{ada.account}, &subs); err != nil || subs != 1 {
			t.Errorf("subscriptions of the account = %d, %v; want 1", subs, err)
		}
		// A stranger finds no such account.
		_, err = e.saasAPI.GetMyPlan(ctx, &plansv1.GetMyPlanRequest{UserId: ben.id, AccountId: ada.account})
		wantCode(t, "another account's plan", err, codes.NotFound, "")
	})

	t.Run("AppsAreLimitedByThePlan", func(t *testing.T) {
		if _, err := e.createApp(ada, "First"); err != nil {
			t.Fatalf("the first app fits Free: %v", err)
		}
		_, err := e.createApp(ada, "Second")
		wantCode(t, "second app on Free", err, codes.ResourceExhausted, "plans.saas.quota.apps")
		if q := quotaOf(e.myPlan(t, ada), "apps"); q.GetUsed() != 1 || !q.GetOverLimit() {
			t.Errorf("apps quota after the first = %v", q)
		}
	})

	t.Run("TheLastAppIsWonByOneOfManyRacers", func(t *testing.T) {
		cat := e.verified(t, "cat@saas.example", "Cat SaaS")
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for i := range 8 {
			wg.Go(func() { _, err := e.createApp(cat, fmt.Sprintf("Race %d", i)); results <- err })
		}
		wg.Wait()
		close(results)
		ok, refused := 0, 0
		for err := range results {
			switch {
			case err == nil:
				ok++
			case code(t, err) == codes.ResourceExhausted:
				refused++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}
		if ok != 1 || refused != 7 {
			t.Errorf("created %d, refused %d; want 1 and 7", ok, refused)
		}
	})

	t.Run("InvitationsHoldSeats", func(t *testing.T) {
		if _, err := e.invite(ada, ada.account, "guest1@saas.example", "viewer"); err != nil {
			t.Fatalf("a second seat fits Free: %v", err)
		}
		// Refreshing the same invitation takes no new seat.
		e.ageInvite(t, ada.account, "guest1@saas.example")
		if _, err := e.invite(ada, ada.account, "guest1@saas.example", "admin"); err != nil {
			t.Fatalf("refreshing an invitation is not a new seat: %v", err)
		}
		_, err := e.invite(ada, ada.account, "guest2@saas.example", "viewer")
		wantCode(t, "a third seat on Free", err, codes.ResourceExhausted, "plans.saas.quota.members")
		if q := quotaOf(e.myPlan(t, ada), "members"); q.GetUsed() != 2 || !q.GetOverLimit() {
			t.Errorf("members quota = %v", q)
		}
		// Revoking the invitation gives the seat back.
		inv, err := e.ident.ListInvitations(ctx, &plansv1.ListInvitationsRequest{UserId: ada.id, AccountId: ada.account})
		if err != nil || len(inv.GetInvitations()) != 1 {
			t.Fatalf("invitations = %v, %v", inv, err)
		}
		if _, err := e.ident.RevokeInvitation(ctx, &plansv1.RevokeInvitationRequest{UserId: ada.id, AccountId: ada.account, InvitationId: inv.GetInvitations()[0].GetId()}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.invite(ada, ada.account, "guest2@saas.example", "viewer"); err != nil {
			t.Errorf("the seat is free again: %v", err)
		}
	})

	t.Run("APIKeysAreLimitedByThePlan", func(t *testing.T) {
		apps, err := e.ident.ListMyApps(ctx, &plansv1.ListMyAppsRequest{UserId: ada.id, AccountId: ada.account})
		if err != nil || len(apps.GetApps()) != 1 {
			t.Fatalf("apps = %v, %v", apps, err)
		}
		appID := apps.GetApps()[0].GetId()
		var ids []string
		for i := range 4 {
			k, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: ada.id, AppId: appID, Environment: plansv1.Environment_ENVIRONMENT_SANDBOX})
			if err != nil {
				t.Fatalf("key %d fits Free: %v", i+1, err)
			}
			ids = append(ids, k.GetApiKey().GetId())
		}
		_, err = e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: ada.id, AppId: appID, Environment: plansv1.Environment_ENVIRONMENT_SANDBOX})
		wantCode(t, "a fifth key on Free", err, codes.ResourceExhausted, "plans.saas.quota.api_keys")
		if _, err := e.ident.RevokeMyApiKey(ctx, &plansv1.RevokeMyApiKeyRequest{UserId: ada.id, AppId: appID, ApiKeyId: ids[0]}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: ada.id, AppId: appID, Environment: plansv1.Environment_ENVIRONMENT_SANDBOX}); err != nil {
			t.Errorf("a revoked key frees its place: %v", err)
		}
	})

	t.Run("UpgradeStartsATrialAndRaisesTheLimits", func(t *testing.T) {
		_, err := e.saasAPI.ChangeMyPlan(ctx, &plansv1.ChangeMyPlanRequest{UserId: ada.id, AccountId: ada.account, PlanCode: "free"})
		wantCode(t, "the plan it is on", err, codes.FailedPrecondition, "plans.saas.same_plan")
		_, err = e.saasAPI.ChangeMyPlan(ctx, &plansv1.ChangeMyPlanRequest{UserId: ada.id, AccountId: ada.account, PlanCode: "platinum"})
		wantCode(t, "an unknown plan", err, codes.InvalidArgument, "plans.saas.unknown_plan")

		before := e.myPlan(t, ada)
		if trial := before.GetOffers()[1].GetTrialDays(); trial != 14 {
			t.Errorf("Pro offers %d trial days, want 14", trial)
		}
		r, err := e.saasAPI.ChangeMyPlan(ctx, &plansv1.ChangeMyPlanRequest{UserId: ada.id, AccountId: ada.account, PlanCode: "pro"})
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		p := r.GetPlan()
		if p.GetPlan().GetCode() != "pro" || p.GetState() != "trialing" || p.GetTrialEndTime() == nil {
			t.Fatalf("after the upgrade: %v", p)
		}
		if q := quotaOf(p, "apps"); q.GetLimit() != 5 || q.GetOverLimit() {
			t.Errorf("apps quota on Pro = %v", q)
		}
		if p.GetOffers()[1].GetTrialDays() != 0 {
			t.Error("a second trial is offered")
		}
		if _, err := e.createApp(ada, "Second"); err != nil {
			t.Errorf("the second app fits Pro: %v", err)
		}
		viewer := e.joinNew(t, ada, "viewer@saas.example", "viewer")
		_, err = e.saasAPI.ChangeMyPlan(ctx, &plansv1.ChangeMyPlanRequest{UserId: viewer.id, AccountId: ada.account, PlanCode: "scale"})
		wantCode(t, "a viewer changing the plan", err, codes.PermissionDenied, "identity.account.forbidden")
		if vp := e.myPlan(t, viewer); vp.GetCanManage() || vp.GetPlan().GetCode() != "pro" {
			t.Errorf("a viewer sees the plan but may not change it: %v", vp)
		}
		// Exactly one subscription is in force, and the Free one ended.
		var open int
		if err := e.scan(ctx, e.platformOf(t).GetId(), `SELECT count(*) FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id WHERE c.external_id = $1 AND s.state <> 'cancelled' AND upper_inf(s.active)`, []any{ada.account}, &open); err != nil || open != 1 {
			t.Errorf("open subscriptions = %d, %v; want 1", open, err)
		}
		// Two apps do not fit Free again.
		_, err = e.saasAPI.ChangeMyPlan(ctx, &plansv1.ChangeMyPlanRequest{UserId: ada.id, AccountId: ada.account, PlanCode: "free"})
		wantCode(t, "downgrade with two apps", err, codes.FailedPrecondition, "plans.saas.downgrade_blocked")
		if e.myPlan(t, ada).GetPlan().GetCode() != "pro" {
			t.Error("a refused downgrade changed the plan")
		}
	})

	t.Run("DowngradeThatFitsWorksAndATrialIsGivenOnce", func(t *testing.T) {
		if _, err := e.createApp(ben, "Only"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.saasAPI.ChangeMyPlan(ctx, &plansv1.ChangeMyPlanRequest{UserId: ben.id, AccountId: ben.account, PlanCode: "pro"}); err != nil {
			t.Fatal(err)
		}
		r, err := e.saasAPI.ChangeMyPlan(ctx, &plansv1.ChangeMyPlanRequest{UserId: ben.id, AccountId: ben.account, PlanCode: "free"})
		if err != nil || r.GetPlan().GetPlan().GetCode() != "free" || r.GetPlan().GetState() != "active" {
			t.Fatalf("downgrade = %v, %v", r, err)
		}
		r, err = e.saasAPI.ChangeMyPlan(ctx, &plansv1.ChangeMyPlanRequest{UserId: ben.id, AccountId: ben.account, PlanCode: "pro"})
		if err != nil {
			t.Fatal(err)
		}
		if r.GetPlan().GetState() != "active" || r.GetPlan().GetTrialEndTime() != nil {
			t.Errorf("the second move to Pro got a trial again: state %s", r.GetPlan().GetState())
		}
	})

	t.Run("TheOperatorSeesAndOverrides", func(t *testing.T) {
		_, err := e.saasAPI.ListOrganizations(ctx, &plansv1.ListOrganizationsRequest{})
		wantCode(t, "a person listing organisations", err, codes.PermissionDenied, "identity.operator.forbidden")
		_, err = e.saasAPI.SetOrganizationPlan(ctx, &plansv1.SetOrganizationPlanRequest{AccountId: ada.account, PlanCode: "scale"})
		wantCode(t, "a person overriding a plan", err, codes.PermissionDenied, "identity.operator.forbidden")

		list, err := e.saasAPI.ListOrganizations(operator(), &plansv1.ListOrganizationsRequest{PageSize: 200})
		if err != nil {
			t.Fatal(err)
		}
		var row *plansv1.OrganizationRow
		for _, o := range list.GetOrganizations() {
			if o.GetAccountId() == ada.account {
				row = o
			}
			if o.GetName() == "Payrudder platform" {
				t.Error("the platform's own account is listed")
			}
		}
		if row == nil || row.GetPlanCode() != "pro" || row.GetOwnerEmail() != "ada@saas.example" || row.GetApps() != 2 || row.GetEventsLimit() != 1_000_000 {
			t.Fatalf("row = %v", row)
		}
		found, err := e.saasAPI.ListOrganizations(operator(), &plansv1.ListOrganizationsRequest{Query: "ben@saas"})
		if err != nil || len(found.GetOrganizations()) != 1 || found.GetOrganizations()[0].GetAccountId() != ben.account {
			t.Errorf("search by owner = %v, %v", found, err)
		}
		page, err := e.saasAPI.ListOrganizations(operator(), &plansv1.ListOrganizationsRequest{PageSize: 1})
		if err != nil || len(page.GetOrganizations()) != 1 || page.GetNextPageToken() == "" {
			t.Errorf("first page = %v, %v", page, err)
		}

		// A manual move needs no checkout and no trial, and ignores the usage.
		r, err := e.saasAPI.SetOrganizationPlan(operator(), &plansv1.SetOrganizationPlanRequest{AccountId: ada.account, PlanCode: "free"})
		if err != nil || r.GetPlan().GetPlan().GetCode() != "free" || r.GetPlan().GetState() != "active" {
			t.Fatalf("operator moves to Free: %v, %v", r, err)
		}
		// Limits of its own replace the plan's.
		r, err = e.saasAPI.SetOrganizationPlan(operator(), &plansv1.SetOrganizationPlanRequest{AccountId: ada.account, Limits: map[string]int64{"apps": 0, "members": 7}})
		if err != nil {
			t.Fatal(err)
		}
		if q := quotaOf(r.GetPlan(), "apps"); !q.GetUnlimited() || !q.GetOverridden() {
			t.Errorf("apps after the override = %v", q)
		}
		if q := quotaOf(r.GetPlan(), "members"); q.GetLimit() != 7 || !q.GetOverridden() {
			t.Errorf("members after the override = %v", q)
		}
		if !r.GetPlan().GetOverridden() {
			t.Error("the plan is not marked as overridden")
		}
		if _, err := e.createApp(ada, "Third"); err != nil {
			t.Errorf("an unlimited override lets another app in: %v", err)
		}
		if _, err := e.saasAPI.ClearOrganizationOverrides(operator(), &plansv1.ClearOrganizationOverridesRequest{AccountId: ada.account}); err != nil {
			t.Fatal(err)
		}
		if p := e.myPlan(t, ada); p.GetOverridden() || quotaOf(p, "apps").GetLimit() != 1 {
			t.Errorf("after clearing: %v", p)
		}
		_, err = e.saasAPI.SetOrganizationPlan(operator(), &plansv1.SetOrganizationPlanRequest{AccountId: ada.account, Limits: map[string]int64{"seats": 3}})
		wantCode(t, "an unknown resource", err, codes.InvalidArgument, "plans.saas.bad_resource")
		_, err = e.saasAPI.SetOrganizationPlan(operator(), &plansv1.SetOrganizationPlanRequest{AccountId: ada.account})
		wantCode(t, "nothing to set", err, codes.InvalidArgument, "")
		var platformAccount string
		if err := e.system.QueryRow(ctx, `SELECT id::text FROM accounts WHERE platform`).Scan(&platformAccount); err != nil {
			t.Fatal(err)
		}
		_, err = e.saasAPI.SetOrganizationPlan(operator(), &plansv1.SetOrganizationPlanRequest{AccountId: platformAccount, PlanCode: "pro"})
		wantCode(t, "a plan for the platform itself", err, codes.InvalidArgument, "plans.saas.platform_account")
	})

	t.Run("IngestPolicyMetersLiveTenantsOnly", func(t *testing.T) {
		apps, err := e.ident.ListMyApps(ctx, &plansv1.ListMyAppsRequest{UserId: ben.id, AccountId: ben.account})
		if err != nil || len(apps.GetApps()) != 1 {
			t.Fatalf("apps = %v, %v", apps, err)
		}
		var live, sandbox string
		for _, tn := range apps.GetApps()[0].GetTenants() {
			if tn.GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE {
				live = tn.GetId()
			} else {
				sandbox = tn.GetId()
			}
		}
		pol, err := e.saasAPI.ResolveIngestPolicy(ctx, &plansv1.ResolveIngestPolicyRequest{TenantId: live})
		if err != nil || !pol.GetMetered() || pol.GetAccountId() != ben.account || pol.GetMeterCode() != "events" || pol.GetCustomerExternalId() != ben.account ||
			pol.GetCustomerId() == "" || pol.GetPlatform().GetId() != e.platformOf(t).GetId() {
			t.Fatalf("live policy = %v, %v", pol, err)
		}
		if pol, err := e.saasAPI.ResolveIngestPolicy(ctx, &plansv1.ResolveIngestPolicyRequest{TenantId: sandbox}); err != nil || pol.GetMetered() {
			t.Errorf("a sandbox is not metered: %v, %v", pol, err)
		}
		if pol, err := e.saasAPI.ResolveIngestPolicy(ctx, &plansv1.ResolveIngestPolicyRequest{TenantId: e.platformOf(t).GetId()}); err != nil || pol.GetMetered() {
			t.Errorf("the platform tenant does not meter itself: %v, %v", pol, err)
		}
		_, err = e.saasAPI.ResolveIngestPolicy(ctx, &plansv1.ResolveIngestPolicyRequest{TenantId: "nope"})
		wantCode(t, "a malformed tenant id", err, codes.InvalidArgument, "")
	})

	t.Run("TheSweepMakesEveryAccountACustomer", func(t *testing.T) {
		id := e.account(t, "Swept")[plansv1.Environment_ENVIRONMENT_LIVE].GetAccountId()
		pt := e.platformOf(t)
		if _, err := e.plans.GetCustomer(as(pt.GetId()), &plansv1.GetCustomerRequest{ExternalId: id}); err == nil {
			t.Fatal("the account was a customer before the sweep")
		}
		// The database holds the accounts of every earlier test: sweep until a pass has nothing left to take.
		for range 50 {
			n, err := e.saas.SweepOnce(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n < 50 {
				break
			}
		}
		if _, err := e.plans.GetCustomer(as(pt.GetId()), &plansv1.GetCustomerRequest{ExternalId: id}); err != nil {
			t.Errorf("the sweep did not make the account a customer: %v", err)
		}
		var subs int
		if err := e.scan(ctx, pt.GetId(), `SELECT count(*) FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id WHERE c.external_id = $1 AND s.state <> 'cancelled'`, []any{id}, &subs); err != nil || subs != 1 {
			t.Errorf("subscriptions after the sweep = %d, %v", subs, err)
		}
		// Sweeping again is harmless: the account keeps its one subscription.
		if _, err := e.saas.SweepOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if err := e.scan(ctx, pt.GetId(), `SELECT count(*) FROM subscriptions s JOIN customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id WHERE c.external_id = $1 AND s.state <> 'cancelled'`, []any{id}, &subs); err != nil || subs != 1 {
			t.Errorf("subscriptions after a second sweep = %d, %v", subs, err)
		}
	})

	t.Run("OtherTenantsNeverSeeThePlatform", func(t *testing.T) {
		apps, _ := e.ident.ListMyApps(ctx, &plansv1.ListMyAppsRequest{UserId: ben.id, AccountId: ben.account})
		var mine string
		for _, tn := range apps.GetApps()[0].GetTenants() {
			if tn.GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE {
				mine = tn.GetId()
			}
		}
		pl, err := e.plans.ListPlans(as(mine), &plansv1.ListPlansRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pl.GetPlans() {
			if p.GetCode() == "free" || p.GetCode() == "pro" || p.GetCode() == "scale" {
				t.Errorf("an ordinary tenant sees the platform plan %s", p.GetCode())
			}
		}
		cs, err := e.plans.ListCustomers(as(mine), &plansv1.ListCustomersRequest{})
		if err != nil || len(cs.GetCustomers()) != 0 {
			t.Errorf("an ordinary tenant sees %d customers of the platform: %v", len(cs.GetCustomers()), err)
		}
		if _, err := e.plans.GetCustomer(as(mine), &plansv1.GetCustomerRequest{ExternalId: ada.account}); err == nil {
			t.Error("an ordinary tenant reads an organisation's customer row of the platform")
		}
	})
}

// myPlanQuiet is myPlan for goroutines: it returns the error instead of failing the test.
func (e *env) myPlanQuiet(p person) error {
	_, err := e.saasAPI.GetMyPlan(context.Background(), &plansv1.GetMyPlanRequest{UserId: p.id, AccountId: p.account})
	return err
}

// testSaasOff: with the mode off (the default) nothing is limited and the plan calls say so.
func testSaasOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.verified(t, "off@saas.example", "Off SaaS")
	for i := range 3 {
		if _, err := e.createApp(p, fmt.Sprintf("App %d", i)); err != nil {
			t.Fatalf("app %d with SaaS off: %v", i, err)
		}
	}
	r, err := e.saasAPI.GetMyPlan(ctx, &plansv1.GetMyPlanRequest{UserId: p.id, AccountId: p.account})
	if err != nil || r.GetPlan().GetEnabled() {
		t.Errorf("GetMyPlan with SaaS off = %v, %v", r, err)
	}
	if g, err := e.saasAPI.GetPlatform(ctx, &plansv1.GetPlatformRequest{}); err != nil || g.GetEnabled() {
		t.Errorf("GetPlatform with SaaS off = %v, %v", g, err)
	}
	_, err = e.saasAPI.ChangeMyPlan(ctx, &plansv1.ChangeMyPlanRequest{UserId: p.id, AccountId: p.account, PlanCode: "pro"})
	wantCode(t, "changing a plan with SaaS off", err, codes.FailedPrecondition, "plans.saas.disabled")
	pol, err := e.saasAPI.ResolveIngestPolicy(ctx, &plansv1.ResolveIngestPolicyRequest{TenantId: "00000000-0000-0000-0000-000000000001"})
	if err != nil || pol.GetMetered() {
		t.Errorf("ingest policy with SaaS off = %v, %v", pol, err)
	}
	_, err = e.saasAPI.ListOrganizations(operator(), &plansv1.ListOrganizationsRequest{})
	wantCode(t, "operator list with SaaS off", err, codes.FailedPrecondition, "plans.saas.disabled")
}
