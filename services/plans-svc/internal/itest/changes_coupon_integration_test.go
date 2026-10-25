//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
)

// The unused time of a discounted fee is a share of what was paid, not of the list fee: a prorated change credits half of what a customer with a 50 % coupon
// paid, and charges half of what the new plan costs for the rest of the period, as the coupon carries over.
func testPlanChangeWithACouponIsPricedAfterTheCoupon(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Change with a coupon")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	for _, p := range []struct {
		code string
		fee  int64
	}{{"basic", 3000}, {"pro", 9000}} {
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: p.code, Name: p.code}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: p.code, BaseFeeMinor: p.fee}); err != nil {
			t.Fatal(err)
		}
	}
	coupons := []*plansv1.Coupon{
		{Code: "HALF", PercentBps: 5000, Duration: plansv1.CouponDuration_COUPON_DURATION_FOREVER},
		{Code: "HALF1", PercentBps: 5000, Duration: plansv1.CouponDuration_COUPON_DURATION_REPEATING, DurationPeriods: 1},
	}
	for _, c := range coupons {
		if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: c}); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	subscribe := func(coupon string) *plansv1.Subscription {
		t.Helper()
		n++
		ext := "cus_chg_coupon_" + string(rune('a'+n))
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "basic", StartTime: timestamppb.New(start), CouponCode: coupon})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
			t.Fatal(err)
		}
		return r.GetSubscription()
	}
	preview := func(id string) (*plansv1.ChangeQuote, error) {
		r, err := e.plans.PreviewSubscriptionChange(ctx, &plansv1.PreviewSubscriptionChangeRequest{
			Change: &plansv1.ChangeSubscriptionRequest{SubscriptionId: id, PlanCode: "pro", Timing: plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY}})
		return r.GetQuote(), err
	}
	near := func(got, want int64) bool { return got >= want-2 && got <= want+2 }

	t.Run("credit and charge are after the coupon", func(t *testing.T) {
		list, err := preview(subscribe("").GetId())
		if err != nil || list.GetCreditMinor() <= 0 || list.GetChargeMinor() <= 0 {
			t.Fatalf("list-price quote = %v, %v", list, err)
		}
		sub := subscribe("HALF")
		q, err := preview(sub.GetId())
		if err != nil {
			t.Fatal(err)
		}
		if !near(q.GetCreditMinor(), list.GetCreditMinor()/2) || !near(q.GetChargeMinor(), list.GetChargeMinor()/2) || q.GetNextRenewalMinor() != 9000 {
			t.Fatalf("quote with a 50 %% coupon = credit %d charge %d renewal %d; want half of %d and %d, and the list renewal 9000",
				q.GetCreditMinor(), q.GetChargeMinor(), q.GetNextRenewalMinor(), list.GetCreditMinor(), list.GetChargeMinor())
		}
		// The change that is made is the one that was quoted.
		r, err := e.plans.ChangeSubscription(ctx, &plansv1.ChangeSubscriptionRequest{SubscriptionId: sub.GetId(), PlanCode: "pro", Timing: plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY})
		if err != nil || !near(r.GetQuote().GetCreditMinor(), q.GetCreditMinor()) || r.GetSubscription().GetCouponCode() != "HALF" {
			t.Fatalf("change = %v, %v", r, err)
		}
		adj, err := e.plans.ListSubscriptionAdjustments(ctx, &plansv1.ListSubscriptionAdjustmentsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, a := range adj.GetAdjustments() {
			if a.GetSubscription().GetId() == r.GetSubscription().GetId() {
				found = near(a.GetCreditMinor(), q.GetCreditMinor()) && near(a.GetChargeMinor(), q.GetChargeMinor())
			}
		}
		if !found {
			t.Errorf("no adjustment of %d credit and %d charge for the change", q.GetCreditMinor(), q.GetChargeMinor())
		}
	})

	t.Run("a coupon that has used up its periods cannot say what the period in force paid", func(t *testing.T) {
		sub := subscribe("HALF1")
		if _, err := preview(sub.GetId()); code(t, err) != codes.FailedPrecondition || errkey.Of(err) != "plans.subscription.coupon_blocks_credit" {
			t.Fatalf("preview with an used up coupon: %v (key %q), want FAILED_PRECONDITION coupon_blocks_credit", err, errkey.Of(err))
		}
		later := &plansv1.ChangeSubscriptionRequest{SubscriptionId: sub.GetId(), PlanCode: "pro", Timing: plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END}
		if res, err := e.plans.ChangeSubscription(ctx, later); err != nil || !res.GetScheduled() {
			t.Fatalf("change at the period end = %v, %v; it moves no money, so it stays possible", res, err)
		}
	})
}
