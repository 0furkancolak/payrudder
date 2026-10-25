//go:build integration

package itest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func changeOf(r *plansv1.PromoteMyAppResponse, family, key string) *plansv1.PromoteChange {
	for _, c := range r.GetChanges() {
		if c.GetFamily() == family && c.GetKey() == key {
			return c
		}
	}
	return nil
}

func hasAction(t *testing.T, r *plansv1.PromoteMyAppResponse, family, key string, want plansv1.PromoteChange_Action) *plansv1.PromoteChange {
	t.Helper()
	c := changeOf(r, family, key)
	if c == nil || c.GetAction() != want {
		t.Fatalf("%s/%s = %v, want %v (all: %v)", family, key, c, want, r.GetChanges())
	}
	return c
}

// testPromote: configuration goes from an app's sandbox to its live environment, only adds, never copies a customer, a secret or a conflicting object, shows a
// diff first, applies the diff it showed, can be run again, and leaves a trail.
func testPromote(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.verified(t, "promote-owner@example.com", "Promote Co")
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: owner.id, AccountId: owner.account, Name: "Promo", Currency: "TRY", Timezone: "Europe/Istanbul"})
	if err != nil {
		t.Fatal(err)
	}
	app := created.GetApp()
	live, sandbox := envOf(app, plansv1.Environment_ENVIRONMENT_LIVE), envOf(app, plansv1.Environment_ENVIRONMENT_SANDBOX)
	seed(t, e, sandbox.GetId(), "sbx") // a customer, a subscription, a meter, a plan, tax, a coupon, mail settings, a webhook endpoint
	promote := func(uid string, dry bool, hash string) (*plansv1.PromoteMyAppResponse, error) {
		return e.ident.PromoteMyApp(ctx, &plansv1.PromoteMyAppRequest{UserId: uid, AppId: app.GetId(), DryRun: dry, PlanHash: hash})
	}
	liveFingerprint := func() map[string]string { return fingerprint(t, e, live.GetId()) }

	// Who may.
	viewer := e.signUp(t, "promote-viewer@example.com", "Viewer Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'viewer')`, owner.account, viewer.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	_, err = promote(viewer.GetUser().GetId(), true, "")
	wantCode(t, "a viewer", err, codes.PermissionDenied, "identity.account.forbidden")
	stranger := e.verified(t, "promote-stranger@example.com", "Stranger Co")
	_, err = promote(stranger.id, true, "")
	wantCode(t, "a stranger", err, codes.NotFound, "identity.app.not_found")

	// The diff: everything is new in live, and looking at it changes nothing.
	before := liveFingerprint()
	dry, err := promote(owner.id, true, "")
	if err != nil {
		t.Fatal(err)
	}
	hasAction(t, dry, "meters", "gb_hours", plansv1.PromoteChange_ACTION_CREATE)
	hasAction(t, dry, "plans", "pro", plansv1.PromoteChange_ACTION_CREATE)
	hasAction(t, dry, "taxes", "TR", plansv1.PromoteChange_ACTION_CREATE)
	hasAction(t, dry, "coupons", "SEED", plansv1.PromoteChange_ACTION_CREATE)
	hasAction(t, dry, "mail", "settings", plansv1.PromoteChange_ACTION_CREATE)
	skip := hasAction(t, dry, "webhooks", "https://example.com/hook", plansv1.PromoteChange_ACTION_SKIPPED)
	if skip.GetDetail() == "" {
		t.Error("a skip says why")
	}
	if !dry.GetDryRun() || dry.GetPlanHash() == "" || dry.GetCreated() != 5 || dry.GetSkipped() != 1 {
		t.Fatalf("dry run = %v", dry)
	}
	if !equalFingerprints(before, liveFingerprint()) {
		t.Fatalf("a dry run changed live: %s", diffFingerprints(before, liveFingerprint()))
	}
	if n := promotions(t, e, app.GetId()); n != 0 {
		t.Errorf("a dry run left %d audit rows", n)
	}

	// Apply what was shown.
	done, err := promote(owner.id, false, dry.GetPlanHash())
	if err != nil {
		t.Fatal(err)
	}
	if done.GetCreated() != 5 || done.GetFailed() != 0 {
		t.Fatalf("applied = %v", done)
	}
	for _, c := range done.GetChanges() {
		if c.GetAction() == plansv1.PromoteChange_ACTION_CREATE && (!c.GetApplied() || c.GetError() != "") {
			t.Errorf("not applied: %v", c)
		}
	}
	lctx := as(live.GetId())
	if m, err := e.plans.GetMeter(lctx, &plansv1.GetMeterRequest{Code: "gb_hours"}); err != nil || m.GetMeter().GetUnitLabel() != "GB-h" {
		t.Errorf("live meter = %v, %v", m, err)
	}
	if v, err := e.plans.GetPlanVersion(lctx, &plansv1.GetPlanVersionRequest{PlanCode: "pro"}); err != nil || v.GetPlanVersion().GetBaseFeeMinor() != 9900 || len(v.GetPlanVersion().GetPrices()) != 1 {
		t.Errorf("live plan version = %v, %v", v, err)
	}
	if r, err := e.plans.GetTaxRates(lctx, &plansv1.GetTaxRatesRequest{}); err != nil || len(r.GetRates()) != 1 || r.GetRates()[0].GetRateBps() != 2000 {
		t.Errorf("live tax = %v, %v", r, err)
	}
	if r, err := e.plans.ListCoupons(lctx, &plansv1.ListCouponsRequest{}); err != nil || len(r.GetCoupons()) != 1 || r.GetCoupons()[0].GetCode() != "SEED" {
		t.Errorf("live coupons = %v, %v", r, err)
	}
	if r, err := e.plans.GetMailSettings(lctx, &plansv1.GetMailSettingsRequest{}); err != nil {
		t.Errorf("live mail = %v", err)
	} else {
		for _, c := range r.GetCategories() {
			if c.GetCategory() == "usage_alerts" && c.GetEnabled() {
				t.Error("the sandbox switched usage_alerts off; live did not follow")
			}
		}
	}

	// Never customers, subscriptions or webhook endpoints (live is not active).
	if r, err := e.plans.ListCustomers(lctx, &plansv1.ListCustomersRequest{}); err != nil || len(r.GetCustomers()) != 0 {
		t.Errorf("customers in live after a promotion: %v, %v", r, err)
	}
	if r, err := e.plans.ListSubscriptions(lctx, &plansv1.ListSubscriptionsRequest{}); err != nil || len(r.GetSubscriptions()) != 0 {
		t.Errorf("subscriptions in live after a promotion: %v, %v", r, err)
	}
	if r, err := e.hooks.ListWebhookEndpoints(lctx, &plansv1.ListWebhookEndpointsRequest{}); err != nil || len(r.GetEndpoints()) != 0 {
		t.Errorf("webhook endpoints in an inactive live: %v, %v", r, err)
	}
	if n := promotions(t, e, app.GetId()); n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}

	// Again: nothing to do.
	again, err := promote(owner.id, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.GetCreated() != 0 || again.GetFailed() != 0 || again.GetConflicts() != 0 || again.GetUnchanged() != 5 {
		t.Fatalf("second run = %v", again)
	}

	// A changed live object is a conflict, shown and left alone; so is a plan the sandbox moved on.
	if _, err := e.plans.SetTaxRates(as(sandbox.GetId()), &plansv1.SetTaxRatesRequest{Rates: []*plansv1.TaxRate{{Country: "TR", Name: "KDV", RateBps: 1800}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(as(sandbox.GetId()), &plansv1.PublishPlanVersionRequest{PlanCode: "pro", BaseFeeMinor: 12900,
		Prices: []*plansv1.Price{{MeterCode: "gb_hours", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "1"}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateMeter(as(sandbox.GetId()), &plansv1.CreateMeterRequest{Code: "api_calls", Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: "calls"}); err != nil {
		t.Fatal(err)
	}
	pre := liveFingerprint()
	diverged, err := promote(owner.id, false, "")
	if err != nil {
		t.Fatal(err)
	}
	hasAction(t, diverged, "taxes", "TR", plansv1.PromoteChange_ACTION_CONFLICT)
	hasAction(t, diverged, "plans", "pro", plansv1.PromoteChange_ACTION_CONFLICT)
	hasAction(t, diverged, "meters", "api_calls", plansv1.PromoteChange_ACTION_CREATE)
	if r, _ := e.plans.GetTaxRates(lctx, &plansv1.GetTaxRatesRequest{}); r.GetRates()[0].GetRateBps() != 2000 {
		t.Error("a conflicting tax rate was overwritten")
	}
	if v, _ := e.plans.GetPlanVersion(lctx, &plansv1.GetPlanVersionRequest{PlanCode: "pro"}); v.GetPlanVersion().GetVersion() != 1 {
		t.Error("a conflicting plan got a new version in live")
	}
	if equalFingerprints(pre, liveFingerprint()) {
		t.Error("the one create of that run (a meter) did not happen")
	}

	// A preview that went stale is refused.
	preview, err := promote(owner.id, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateMeter(as(sandbox.GetId()), &plansv1.CreateMeterRequest{Code: "late_meter", Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: "x"}); err != nil {
		t.Fatal(err)
	}
	_, err = promote(owner.id, false, preview.GetPlanHash())
	wantCode(t, "stale preview", err, codes.FailedPrecondition, "identity.promote.stale")

	// Once live is active the webhook endpoint follows, with a secret of its own.
	e.activate(t, live.GetId())
	withHook, err := promote(owner.id, false, "")
	if err != nil {
		t.Fatal(err)
	}
	hook := hasAction(t, withHook, "webhooks", "https://example.com/hook", plansv1.PromoteChange_ACTION_CREATE)
	if !hook.GetApplied() || len(hook.GetSecret()) < 10 {
		t.Errorf("webhook change = %v: it should be made, with a new secret", hook)
	}
	if r, err := e.hooks.ListWebhookEndpoints(lctx, &plansv1.ListWebhookEndpointsRequest{}); err != nil || len(r.GetEndpoints()) != 1 || r.GetEndpoints()[0].GetSecret() != "" {
		t.Errorf("live endpoints = %v, %v", r, err)
	}
	if n := promotions(t, e, app.GetId()); n != 4 {
		t.Errorf("audit rows = %d, want 4 applied runs", n)
	}

	// The database allows one direction only.
	if _, err := e.system.Exec(ctx, `INSERT INTO config_promotions (id, app_id, user_id, sandbox_tenant_id, live_tenant_id, created, unchanged, conflicts, skipped, failed, detail)
		VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3::uuid, $4::uuid, 0,0,0,0,0,'[]')`, app.GetId(), owner.id, live.GetId(), sandbox.GetId()); err == nil {
		t.Error("a promotion from live to the sandbox was recorded")
	}
}

func promotions(t *testing.T, e *env, appID string) int {
	t.Helper()
	var n int
	if err := e.system.QueryRow(context.Background(), `SELECT count(*) FROM config_promotions WHERE app_id = $1::uuid`, appID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
