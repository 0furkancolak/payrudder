//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func testCancelCredit(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Cancel credit")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 3000}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: "HALF", PercentBps: 5000, Duration: plansv1.CouponDuration_COUPON_DURATION_FOREVER}}); err != nil {
		t.Fatal(err)
	}
	n := 0
	subscribe := func(timing plansv1.FeeTiming, coupon string) *plansv1.Subscription {
		t.Helper()
		n++
		ext := "cus_cc_" + string(rune('a'+n))
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "basic", StartTime: timestamppb.New(start), FeeTiming: timing, CouponCode: coupon})
		if err != nil {
			t.Fatal(err)
		}
		if timing != plansv1.FeeTiming_FEE_TIMING_ARREARS {
			if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
				t.Fatal(err)
			}
		}
		return r.GetSubscription()
	}
	adjustmentsOf := func(id string) []*plansv1.Adjustment {
		t.Helper()
		l, err := e.plans.ListSubscriptionAdjustments(ctx, &plansv1.ListSubscriptionAdjustmentsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var out []*plansv1.Adjustment
		for _, a := range l.GetAdjustments() {
			if a.GetSubscription().GetId() == id {
				out = append(out, a)
			}
		}
		return out
	}

	t.Run("ending a prepaid subscription can credit the unused part", func(t *testing.T) {
		sub := subscribe(plansv1.FeeTiming_FEE_TIMING_ADVANCE, "")
		r, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: sub.GetId(), CreditUnused: true})
		if err != nil {
			t.Fatal(err)
		}
		// 10 of about 30 days used: a bit under 2/3 of 3000 is left (28–31 day months).
		if c := r.GetCreditedMinor(); c < 1900 || c > 2100 {
			t.Fatalf("credited = %d, want about 2000", c)
		}
		adj := adjustmentsOf(sub.GetId())
		if len(adj) != 1 || adj[0].GetCreditMinor() != r.GetCreditedMinor() || adj[0].GetChargeMinor() != 0 {
			t.Fatalf("adjustments = %v", adj)
		}
	})
	t.Run("without the flag nothing is credited", func(t *testing.T) {
		sub := subscribe(plansv1.FeeTiming_FEE_TIMING_ADVANCE, "")
		r, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: sub.GetId()})
		if err != nil || r.GetCreditedMinor() != 0 || len(adjustmentsOf(sub.GetId())) != 0 {
			t.Fatalf("cancel = %v, %v, adjustments %v", r, err, adjustmentsOf(sub.GetId()))
		}
	})
	t.Run("the credit is refused where it cannot be right", func(t *testing.T) {
		arrears := subscribe(plansv1.FeeTiming_FEE_TIMING_ARREARS, "")
		if _, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: arrears.GetId(), CreditUnused: true}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("arrears: %v", err)
		}
		coupon := subscribe(plansv1.FeeTiming_FEE_TIMING_ADVANCE, "HALF")
		if _, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: coupon.GetId(), CreditUnused: true}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("coupon: %v", err)
		}
		// A refused cancel leaves the subscription running.
		if g, _ := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: coupon.GetId()}); g.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE {
			t.Errorf("state after a refused cancel = %v", g.GetSubscription().GetState())
		}
		later := subscribe(plansv1.FeeTiming_FEE_TIMING_ADVANCE, "")
		if _, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: later.GetId(), CreditUnused: true, EndTime: timestamppb.New(time.Now().Add(48 * time.Hour))}); code(t, err) != codes.InvalidArgument {
			t.Errorf("future end: %v", err)
		}
	})
}
