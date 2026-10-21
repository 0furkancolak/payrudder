//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
)

// testManyCurrenciesAndZones: an app whose own currency is TRY sells one plan in zero-, two- and three-decimal currencies, each customer is billed
// in one currency, and accounts can be created in any ISO 4217 currency and IANA zone (also one that is not on the hour).
func testManyCurrenciesAndZones(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ currency, zone string }{
		{"BRL", "America/Sao_Paulo"}, {"INR", "Asia/Kolkata"}, {"KRW", "Asia/Seoul"}, {"BHD", "Asia/Bahrain"}, {"NPR", "Asia/Kathmandu"}, {"ZAR", "Pacific/Chatham"},
	} {
		resp, err := e.admin.CreateAccount(operator(), &plansv1.CreateAccountRequest{Name: "Acct " + c.currency, Currency: c.currency, Timezone: c.zone,
			GracePeriod: durationpb.New(time.Hour), Tier: plansv1.TenantTier_TENANT_TIER_STANDARD})
		if err != nil || resp.GetAccount().GetTenants()[0].GetCurrency() != c.currency || resp.GetAccount().GetTenants()[0].GetTimezone() != c.zone {
			t.Fatalf("account in %s %s = %v, %v", c.currency, c.zone, resp, err)
		}
	}
	for _, bad := range []struct{ currency, zone string }{{"XAU", "UTC"}, {"BGN", "UTC"}, {"usd", "UTC"}, {"USD", "Mars/Olympus"}, {"USD", "Local"}} {
		_, err := e.admin.CreateAccount(operator(), &plansv1.CreateAccountRequest{Name: "Bad", Currency: bad.currency, Timezone: bad.zone,
			GracePeriod: durationpb.New(time.Hour)})
		if code(t, err) != codes.InvalidArgument {
			t.Errorf("account %+v: %v", bad, err)
		}
	}

	live := e.account(t, "Many currencies")[plansv1.Environment_ENVIRONMENT_LIVE] // tenant currency TRY
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "global", Name: "Global"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "global", BaseFeeMinor: 9900, Fees: []*plansv1.CurrencyFee{
		{Currency: "KRW", BaseFeeMinor: 9900}, {Currency: "KWD", BaseFeeMinor: 3500}, {Currency: "BRL", BaseFeeMinor: 4900}, {Currency: "USD", BaseFeeMinor: 999}}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"KRW", "KWD", "BRL", "USD", "TRY"} {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "c-" + c, Name: c, Email: c + "@example.com", BillingCurrency: c}); err != nil {
			t.Fatalf("customer %s: %v", c, err)
		}
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "c-" + c, PlanCode: "global"})
		if err != nil || r.GetSubscription().GetCurrency() != c {
			t.Fatalf("subscription for %s = %v, %v", c, r, err)
		}
	}
	// A currency the plan has no fee in is refused, and so is one a named provider cannot take (with a key the console translates).
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "c-INR", Name: "INR", Email: "i@example.com", BillingCurrency: "INR"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "c-INR", PlanCode: "global"}); code(t, err) != codes.FailedPrecondition {
		t.Fatalf("no INR fee: %v", err)
	}
	_, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "c-KRW", PlanCode: "global", Provider: "iyzico"})
	if code(t, err) != codes.FailedPrecondition || errkey.Of(err) != "plans.subscription.provider_currency" {
		t.Fatalf("iyzico cannot charge KRW: %v (key %q)", err, errkey.Of(err))
	}
}
