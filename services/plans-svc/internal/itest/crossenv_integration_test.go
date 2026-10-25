//go:build integration

package itest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// liveOnly is what one row of each family of a tenant is called, under names no row of the other environment has: what a caller of the other environment
// would have to guess.
type liveOnly struct {
	customer, meter, plan, subscription, webhook string
}

func fixtureFor(t *testing.T, e *env, tenantID, tag string) liveOnly {
	t.Helper()
	ctx := as(tenantID)
	f := liveOnly{customer: "cus_" + tag, meter: "m_" + tag, plan: "plan_" + tag}
	must := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: f.customer, Name: "Ada " + tag, Email: tag + "@example.com"}))
	must(e.plans.CreateMeter(ctx, &plansv1.CreateMeterRequest{Code: f.meter, Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: "u"}))
	must(e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: f.plan, Name: "Plan " + tag}))
	must(e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: f.plan, BaseFeeMinor: 500}))
	sub, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: f.customer, PlanCode: f.plan})
	if err != nil {
		t.Fatal(err)
	}
	f.subscription = sub.GetSubscription().GetId()
	hook, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/" + tag})
	if err != nil {
		t.Fatal(err)
	}
	f.webhook = hook.GetEndpoint().GetId()
	return f
}

// reaches runs every way of asking one tenant for a row of the other, by id or by code, and returns the ones that were answered.
func reaches(e *env, ctx context.Context, f liveOnly) []string {
	var found []string
	ok := func(what string, err error) {
		if err == nil {
			found = append(found, what)
		}
	}
	_, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: f.customer})
	ok("GetCustomer", err)
	_, err = e.plans.GetMeter(ctx, &plansv1.GetMeterRequest{Code: f.meter})
	ok("GetMeter", err)
	_, err = e.plans.GetPlanVersion(ctx, &plansv1.GetPlanVersionRequest{PlanCode: f.plan})
	ok("GetPlanVersion", err)
	_, err = e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: f.subscription})
	ok("GetSubscription", err)
	_, err = e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: f.subscription})
	ok("CancelSubscription", err)
	_, err = e.plans.PauseSubscription(ctx, &plansv1.PauseSubscriptionRequest{SubscriptionId: f.subscription})
	ok("PauseSubscription", err)
	_, err = e.plans.SetPaymentMethod(ctx, &plansv1.SetPaymentMethodRequest{ExternalId: f.customer, PaymentMethod: ""})
	ok("SetPaymentMethod", err)
	_, err = e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: f.customer, PlanCode: f.plan})
	ok("CreateSubscription", err)
	_, err = e.hooks.UpdateWebhookEndpoint(ctx, &plansv1.UpdateWebhookEndpointRequest{Id: f.webhook, Url: "https://example.com/x", Active: true})
	ok("UpdateWebhookEndpoint", err)
	_, err = e.hooks.DeleteWebhookEndpoint(ctx, &plansv1.DeleteWebhookEndpointRequest{Id: f.webhook})
	ok("DeleteWebhookEndpoint", err)
	_, err = e.hooks.RotateWebhookSecret(ctx, &plansv1.RotateWebhookSecretRequest{Id: f.webhook})
	ok("RotateWebhookSecret", err)
	_, err = e.hooks.SendTestWebhook(ctx, &plansv1.SendTestWebhookRequest{Id: f.webhook})
	ok("SendTestWebhook", err)
	// Lists show only the caller's own.
	if r, err := e.plans.ListCustomers(ctx, &plansv1.ListCustomersRequest{}); err == nil {
		for _, c := range r.GetCustomers() {
			if c.GetExternalId() == f.customer {
				found = append(found, "ListCustomers")
			}
		}
	}
	if r, err := e.plans.ListMeters(ctx, &plansv1.ListMetersRequest{}); err == nil {
		for _, m := range r.GetMeters() {
			if m.GetCode() == f.meter {
				found = append(found, "ListMeters")
			}
		}
	}
	if r, err := e.plans.ListSubscriptions(ctx, &plansv1.ListSubscriptionsRequest{}); err == nil {
		for _, s := range r.GetSubscriptions() {
			if s.GetId() == f.subscription {
				found = append(found, "ListSubscriptions")
			}
		}
	}
	if r, err := e.hooks.ListWebhookEndpoints(ctx, &plansv1.ListWebhookEndpointsRequest{}); err == nil {
		for _, h := range r.GetEndpoints() {
			if h.GetId() == f.webhook {
				found = append(found, "ListWebhookEndpoints")
			}
		}
	}
	return found
}

// testCrossEnvironmentIDs: an id or a code of one environment sent to the other is "not found", for every resource family, both ways round. The two
// environments of an app are two tenants, so this is the tenant isolation (RLS) seen from the environment side; the point of the test is that no family
// is served by a path that forgets it.
func testCrossEnvironmentIDs(t *testing.T) {
	e := newEnv(t)
	acct := e.account(t, "Cross Env Co")
	live, sandbox := acct[plansv1.Environment_ENVIRONMENT_LIVE].GetId(), acct[plansv1.Environment_ENVIRONMENT_SANDBOX].GetId()
	liveRows := fixtureFor(t, e, live, "live_"+strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	sandboxRows := fixtureFor(t, e, sandbox, "sbx_"+strings.ReplaceAll(uuid.NewString()[:8], "-", ""))

	if found := reaches(e, as(sandbox), liveRows); len(found) != 0 {
		t.Errorf("a sandbox caller reached live rows through: %v", found)
	}
	if found := reaches(e, as(live), sandboxRows); len(found) != 0 {
		t.Errorf("a live caller reached sandbox rows through: %v", found)
	}
	// Each side still sees its own (the test is not passing because nothing works).
	if _, err := e.plans.GetSubscription(as(live), &plansv1.GetSubscriptionRequest{SubscriptionId: liveRows.subscription}); err != nil {
		t.Errorf("live cannot read its own subscription: %v", err)
	}
	if _, err := e.plans.GetCustomer(as(sandbox), &plansv1.GetCustomerRequest{ExternalId: sandboxRows.customer}); err != nil {
		t.Errorf("sandbox cannot read its own customer: %v", err)
	}
	// And the answer is the plain "not found", the same as for an id that never existed.
	_, err := e.plans.GetSubscription(as(sandbox), &plansv1.GetSubscriptionRequest{SubscriptionId: liveRows.subscription})
	wantCode(t, "a live subscription asked for in the sandbox", err, codes.NotFound, "")
	_, err = e.plans.GetCustomer(as(live), &plansv1.GetCustomerRequest{ExternalId: sandboxRows.customer})
	wantCode(t, "a sandbox customer asked for in live", err, codes.NotFound, "")
}

// testSandboxKeyNeverTouchesLive is the property: whatever a caller holding the sandbox key does, in whatever order, no live row is changed and none is read.
// It runs a seeded random walk over the tenant API as the sandbox tenant, with the live tenant's ids mixed into the requests, and compares a fingerprint of every
// tenant-owned table's live rows before and after (taken with the system role, so RLS cannot hide a change from the check).
func testSandboxKeyNeverTouchesLive(t *testing.T) {
	e := newEnv(t)
	acct := e.account(t, "Property Co")
	live, sandbox := acct[plansv1.Environment_ENVIRONMENT_LIVE].GetId(), acct[plansv1.Environment_ENVIRONMENT_SANDBOX].GetId()
	liveRows := fixtureFor(t, e, live, "liveprop")
	seed(t, e, live, "lp") // one row of every other family, too
	before := fingerprint(t, e, live)
	sbxBefore := fingerprint(t, e, sandbox)

	seedValue := rand.Uint64()
	rng := rand.New(rand.NewPCG(seedValue, 0x5eed))
	ctx := as(sandbox)
	var customers, plans []string
	var subs, hooks []string
	n := 0
	next := func(prefix string) string { n++; return fmt.Sprintf("%s_%d", prefix, n) }
	const steps = 400
	for i := range steps {
		switch rng.IntN(14) {
		case 0:
			ext := next("c")
			if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: "N", Email: ext + "@example.com"}); err == nil {
				customers = append(customers, ext)
			}
		case 1:
			_, _ = e.plans.CreateMeter(ctx, &plansv1.CreateMeterRequest{Code: next("m"), Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, UnitLabel: "u"})
		case 2:
			code := next("p")
			if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: code, Name: code}); err == nil {
				if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: code, BaseFeeMinor: int64(rng.IntN(5000))}); err == nil {
					plans = append(plans, code)
				}
			}
		case 3:
			if len(customers) > 0 && len(plans) > 0 {
				if r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customers[rng.IntN(len(customers))], PlanCode: plans[rng.IntN(len(plans))]}); err == nil {
					subs = append(subs, r.GetSubscription().GetId())
				}
			}
		case 4:
			if len(subs) > 0 {
				_, _ = e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: subs[rng.IntN(len(subs))]})
			}
		case 5:
			_, _ = e.plans.SetTaxRates(ctx, &plansv1.SetTaxRatesRequest{Rates: []*plansv1.TaxRate{{Country: "DE", Name: "VAT", RateBps: int32(rng.IntN(3000))}}})
		case 6:
			_, _ = e.plans.SetMailSettings(ctx, &plansv1.SetMailSettingsRequest{Categories: []*plansv1.MailCategorySetting{{Category: "usage_alerts", Enabled: rng.IntN(2) == 0}}})
		case 7:
			if r, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/" + next("h")}); err == nil {
				hooks = append(hooks, r.GetEndpoint().GetId())
			}
		case 8:
			if len(hooks) > 0 {
				_, _ = e.hooks.DeleteWebhookEndpoint(ctx, &plansv1.DeleteWebhookEndpointRequest{Id: hooks[rng.IntN(len(hooks))]})
			}
		// The rest are the live tenant's own names and ids, asked for by a sandbox caller: each must be refused.
		case 9:
			if _, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: liveRows.subscription}); err == nil {
				t.Fatalf("seed %d step %d: a sandbox caller cancelled a live subscription", seedValue, i)
			}
		case 10:
			if _, err := e.hooks.DeleteWebhookEndpoint(ctx, &plansv1.DeleteWebhookEndpointRequest{Id: liveRows.webhook}); err == nil {
				t.Fatalf("seed %d step %d: a sandbox caller deleted a live webhook endpoint", seedValue, i)
			}
		case 11:
			if _, err := e.plans.SetPaymentMethod(ctx, &plansv1.SetPaymentMethodRequest{ExternalId: liveRows.customer}); err == nil {
				t.Fatalf("seed %d step %d: a sandbox caller changed a live customer", seedValue, i)
			}
		case 12:
			if r, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: liveRows.customer}); err == nil {
				t.Fatalf("seed %d step %d: a sandbox caller read a live customer: %v", seedValue, i, r)
			}
		case 13:
			if r, err := e.plans.ListCustomers(ctx, &plansv1.ListCustomersRequest{}); err == nil {
				for _, c := range r.GetCustomers() {
					if c.GetExternalId() == liveRows.customer || strings.HasSuffix(c.GetEmail(), "liveprop@example.com") {
						t.Fatalf("seed %d step %d: a sandbox list holds a live customer", seedValue, i)
					}
				}
			}
		}
	}
	if after := fingerprint(t, e, live); !equalFingerprints(before, after) {
		t.Fatalf("seed %d: live rows changed under a sandbox caller: %s", seedValue, diffFingerprints(before, after))
	}
	if sbxAfter := fingerprint(t, e, sandbox); equalFingerprints(sbxBefore, sbxAfter) {
		t.Fatalf("seed %d: the walk changed nothing in the sandbox: the property would be vacuous", seedValue)
	}
}

// fingerprint is, for every table that holds tenant rows, how many rows the tenant has and a digest of them, read as the system role.
func fingerprint(t *testing.T, e *env, tenantID string) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := e.system.Query(ctx, `SELECT c.table_name FROM information_schema.columns c JOIN information_schema.tables t ON t.table_name = c.table_name AND t.table_schema = c.table_schema
		WHERE c.table_schema = 'public' AND c.column_name = 'tenant_id' AND t.table_type = 'BASE TABLE' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	out := map[string]string{}
	for _, tb := range tables {
		var n int
		var sum *string
		// The table name comes from the catalogue above, never from a caller.
		q := fmt.Sprintf(`SELECT count(*), md5(string_agg(x::text, '|' ORDER BY x::text)) FROM %s x WHERE tenant_id = $1::uuid`, pgIdent(tb))
		if err := e.system.QueryRow(ctx, q, tenantID).Scan(&n, &sum); err != nil {
			// A table the system role may not read (no grant) cannot be touched by it either way; it still has the tenant's RLS.
			continue
		}
		s := ""
		if sum != nil {
			s = *sum
		}
		out[tb] = fmt.Sprintf("%d:%s", n, s)
	}
	if len(out) < 10 {
		t.Fatalf("fingerprint covers only %d tables: %v", len(out), out)
	}
	return out
}

func pgIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func equalFingerprints(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func diffFingerprints(a, b map[string]string) string {
	var d []string
	for k, v := range a {
		if b[k] != v {
			d = append(d, fmt.Sprintf("%s %s -> %s", k, v, b[k]))
		}
	}
	return strings.Join(d, "; ")
}
