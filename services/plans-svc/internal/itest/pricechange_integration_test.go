//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// A price change moves the subscriptions of one plan version to another at their next renewal, only when that renewal is far enough away to tell the customer, and
// names the ones it left alone.
func testPriceChange(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Price change")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	for _, fee := range []int64{3000, 4000} {
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: fee}); err != nil {
			t.Fatal(err)
		}
	}
	subscribe := func(ext string, startedDaysAgo int, timing plansv1.FeeTiming) string {
		t.Helper()
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-time.Duration(startedDaysAgo) * 24 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "basic", PlanVersion: 1, StartTime: timestamppb.New(start), FeeTiming: timing})
		if err != nil {
			t.Fatal(err)
		}
		if timing != plansv1.FeeTiming_FEE_TIMING_ARREARS {
			if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
				t.Fatal(err)
			}
		}
		return r.GetSubscription().GetId()
	}
	far := subscribe("cus_far", 5, plansv1.FeeTiming_FEE_TIMING_ADVANCE)
	near := subscribe("cus_near", 25, plansv1.FeeTiming_FEE_TIMING_ADVANCE)
	arrears := subscribe("cus_arrears", 5, plansv1.FeeTiming_FEE_TIMING_ARREARS)
	reasons := func(r *plansv1.SchedulePriceChangeResponse) map[string]string {
		m := map[string]string{}
		for _, s := range r.GetSkipped() {
			m[s.GetSubscriptionId()] = s.GetReason()
		}
		return m
	}

	t.Run("notice days are bounded and the versions must differ", func(t *testing.T) {
		_, err := e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, NoticeDays: 7})
		wantCode(t, "7 days of notice", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 2, ToVersion: 2})
		wantCode(t, "same version", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "nope", FromVersion: 1})
		wantCode(t, "unknown plan", err, codes.NotFound, "")
	})

	t.Run("a dry run counts and changes nothing", func(t *testing.T) {
		r, err := e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, NoticeDays: 14, DryRun: true})
		if err != nil || r.GetScheduled() != 1 || r.GetScheduledSubscriptionIds()[0] != far {
			t.Fatalf("dry run: %v %v", r, err)
		}
		again, err := e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, NoticeDays: 14, DryRun: true})
		if err != nil || again.GetScheduled() != 1 {
			t.Fatalf("a dry run changed something: %v %v", again, err)
		}
	})

	t.Run("only the renewal far enough away is scheduled; the others are named with why", func(t *testing.T) {
		r, err := e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, NoticeDays: 14})
		if err != nil {
			t.Fatal(err)
		}
		if r.GetScheduled() != 1 || r.GetToVersion() != 2 || r.GetNoticeDays() != 14 {
			t.Fatalf("response: %v", r)
		}
		// The customer's mail is one fact in the outbox, for the one scheduled change.
		var facts int
		if err := e.system.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE topic = 'subscription.price-change.v1'`).Scan(&facts); err != nil || facts != 1 {
			t.Errorf("price-change facts in the outbox: %d %v", facts, err)
		}
		got := reasons(r)
		if got[near] != "renews_soon" || got[arrears] != "not_changeable" || len(got) != 2 {
			t.Errorf("skipped: %v", got)
		}
	})

	t.Run("a subscription with a change waiting is left alone the second time", func(t *testing.T) {
		r, err := e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, NoticeDays: 14})
		if err != nil {
			t.Fatal(err)
		}
		if r.GetScheduled() != 0 || reasons(r)[far] != "has_pending_change" {
			t.Errorf("second run: %v", r)
		}
	})
}
