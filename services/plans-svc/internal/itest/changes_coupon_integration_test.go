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

// The unused time of a discounted fee is not the list fee's share: a prorated change of a subscription with a coupon is refused, as ending it with a credit is,
// instead of crediting more than the customer paid.
func testPlanChangeWithACouponIsNotPricedAtListFee(t *testing.T) {
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
	if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: "HALF", PercentBps: 5000, Duration: plansv1.CouponDuration_COUPON_DURATION_FOREVER}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_chg_coupon", Name: "c", Email: "c@example.com"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Microsecond)
	r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_chg_coupon", PlanCode: "basic", StartTime: timestamppb.New(start), CouponCode: "HALF"})
	if err != nil {
		t.Fatal(err)
	}
	sub := r.GetSubscription()
	if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: sub.GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
		t.Fatal(err)
	}

	now := &plansv1.ChangeSubscriptionRequest{SubscriptionId: sub.GetId(), PlanCode: "pro", Timing: plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY}
	if _, err := e.plans.PreviewSubscriptionChange(ctx, &plansv1.PreviewSubscriptionChangeRequest{Change: now}); code(t, err) != codes.FailedPrecondition || errkey.Of(err) != "plans.subscription.coupon_blocks_credit" {
		t.Fatalf("preview of a prorated change with a coupon: %v (key %q), want FAILED_PRECONDITION coupon_blocks_credit", err, errkey.Of(err))
	}
	if _, err := e.plans.ChangeSubscription(ctx, now); code(t, err) != codes.FailedPrecondition {
		t.Fatalf("prorated change with a coupon: %v, want FAILED_PRECONDITION", err)
	}
	// Nothing was changed, and a change at the end of the period moves no money, so it stays possible.
	if g, _ := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: sub.GetId()}); g.GetSubscription().GetPlanCode() != "basic" {
		t.Fatalf("a refused change moved the subscription to %q", g.GetSubscription().GetPlanCode())
	}
	later := &plansv1.ChangeSubscriptionRequest{SubscriptionId: sub.GetId(), PlanCode: "pro", Timing: plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END}
	if res, err := e.plans.ChangeSubscription(ctx, later); err != nil || !res.GetScheduled() {
		t.Fatalf("change at the period end = %v, %v; want it scheduled", res, err)
	}
}
