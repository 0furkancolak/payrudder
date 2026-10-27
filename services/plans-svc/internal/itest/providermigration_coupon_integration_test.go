//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// A coupon is a discount on a fee Payrudder invoices in advance. A provider-run successor cannot hold it, so a migration to one is refused until the coupon is
// taken off, instead of dropping it without a word; a Payrudder-run successor billed in advance carries it.
func testProviderMigrationDoesNotDropTheCoupon(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Migration with a coupon")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 3000, Fees: yearlyFees("basic", 3000)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: "HALFPM", PercentBps: 5000, Duration: plansv1.CouponDuration_COUPON_DURATION_FOREVER}}); err != nil {
		t.Fatal(err)
	}
	n := 0
	subscribe := func() *plansv1.Subscription {
		t.Helper()
		n++
		ext := "cus_pm_coupon_" + string(rune('a'+n))
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com", Country: "US", BillingCurrency: "USD"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-48 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "basic", Provider: "iyzico", StartTime: timestamppb.New(start), CouponCode: "HALFPM"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
			t.Fatal(err)
		}
		return r.GetSubscription()
	}

	t.Run("a provider-run target is refused while the coupon is on", func(t *testing.T) {
		sub := subscribe()
		_, err := e.plans.StartProviderMigration(ctx, &plansv1.StartProviderMigrationRequest{SubscriptionId: sub.GetId(), ToProvider: "paddle"})
		if code(t, err) != codes.FailedPrecondition || errkey.Of(err) != "plans.migration.coupon_blocks_move" {
			t.Fatalf("migration of a subscription with a coupon to paddle: %v (key %q); want FAILED_PRECONDITION coupon_blocks_move", err, errkey.Of(err))
		}
		got, err := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: sub.GetId()})
		if err != nil || got.GetSubscription().GetCouponCode() != "HALFPM" || got.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE {
			t.Fatalf("subscription after the refusal = %v, %v; want it untouched, coupon on", got.GetSubscription(), err)
		}
		if _, err := e.plans.RemoveCoupon(ctx, &plansv1.RemoveCouponRequest{SubscriptionId: sub.GetId()}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.StartProviderMigration(ctx, &plansv1.StartProviderMigrationRequest{SubscriptionId: sub.GetId(), ToProvider: "paddle"}); err != nil {
			t.Fatalf("migration once the coupon is off: %v", err)
		}
	})

	t.Run("a Payrudder-run target carries it", func(t *testing.T) {
		sub := subscribe()
		st, err := e.plans.StartProviderMigration(ctx, &plansv1.StartProviderMigrationRequest{SubscriptionId: sub.GetId(), ToProvider: "banktransfer"})
		if err != nil || st.GetMigration().GetNewSubscriptionId() == "" {
			t.Fatalf("migration = %v, %v", st, err)
		}
		nw, err := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: st.GetMigration().GetNewSubscriptionId()})
		if err != nil || nw.GetSubscription().GetCouponCode() != "HALFPM" {
			t.Fatalf("successor = %v, %v; want the coupon HALFPM", nw.GetSubscription(), err)
		}
	})
}
