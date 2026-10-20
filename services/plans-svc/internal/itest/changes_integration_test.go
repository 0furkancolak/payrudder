//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

func testSubscriptionChanges(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Changes")[plansv1.Environment_ENVIRONMENT_LIVE] // TRY
	ctx := as(live.GetId())
	year := plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	month := plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
	for _, p := range []struct {
		code string
		fee  int64
	}{{"basic", 3000}, {"pro", 9000}} {
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: p.code, Name: p.code}); err != nil {
			t.Fatal(err)
		}
		// Yearly in the tenant's currency, monthly in dollars (and no yearly fee in dollars).
		fees := []*plansv1.CurrencyFee{{Currency: "TRY", Interval: year, BaseFeeMinor: p.fee * 10}, {Currency: "USD", BaseFeeMinor: p.fee / 30}}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: p.code, BaseFeeMinor: p.fee, Fees: fees}); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	// subscribe creates an advance-billed subscription that started daysAgo days ago, and marks its first fee as invoiced, as billing-svc would.
	subscribe := func(plan string, interval plansv1.SubscriptionInterval, daysAgo int, trialDays int32) *plansv1.Subscription {
		t.Helper()
		n++
		ext := "cus_chg_" + string(rune('a'+n))
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-time.Duration(daysAgo) * 24 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: plan, Interval: interval, StartTime: timestamppb.New(start), TrialDays: trialDays})
		if err != nil {
			t.Fatal(err)
		}
		if trialDays == 0 {
			if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
				t.Fatal(err)
			}
		}
		return r.GetSubscription()
	}
	change := func(id, plan string, interval plansv1.SubscriptionInterval, timing plansv1.ChangeTiming, pr plansv1.ChangeProration) *plansv1.ChangeSubscriptionRequest {
		return &plansv1.ChangeSubscriptionRequest{SubscriptionId: id, PlanCode: plan, Interval: interval, Timing: timing, Proration: pr}
	}
	sameQuote := func(t *testing.T, got, want *plansv1.ChangeQuote) {
		t.Helper()
		if got.GetTiming() != want.GetTiming() || got.GetProration() != want.GetProration() || got.GetNetMinor() != want.GetNetMinor() ||
			got.GetNextRenewalMinor() != want.GetNextRenewalMinor() || got.GetEffectiveTime().AsTime().Sub(want.GetEffectiveTime().AsTime()).Abs() > time.Second {
			t.Fatalf("preview %v and change %v disagree", want, got)
		}
		// Credit and charge are prorated by the second, so the two calls (a moment apart) may differ by a rounding unit at most.
		for _, d := range []int64{got.GetCreditMinor() - want.GetCreditMinor(), got.GetChargeMinor() - want.GetChargeMinor()} {
			if d < -2 || d > 2 {
				t.Fatalf("preview %v and change %v disagree on money", want, got)
			}
		}
	}

	t.Run("an upgrade is immediate, prorated, and the preview is what is then made", func(t *testing.T) {
		old := subscribe("basic", month, 10, 0)
		req := change(old.GetId(), "pro", 0, 0, 0)
		pv, err := e.plans.PreviewSubscriptionChange(ctx, &plansv1.PreviewSubscriptionChangeRequest{Change: req})
		if err != nil {
			t.Fatal(err)
		}
		q := pv.GetQuote()
		if q.GetTiming() != plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY || q.GetProration() != plansv1.ChangeProration_CHANGE_PRORATION_PRORATE ||
			q.GetCreditMinor() <= 0 || q.GetCreditMinor() >= 3000 || q.GetChargeMinor() <= q.GetCreditMinor() || q.GetChargeMinor() >= 9000 ||
			q.GetNetMinor() != q.GetChargeMinor()-q.GetCreditMinor() || q.GetNextRenewalMinor() != 9000 || q.GetCurrency() != "TRY" {
			t.Fatalf("upgrade preview = %v", q)
		}
		if got, _ := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: old.GetId()}); got.GetSubscription().GetPlanCode() != "basic" {
			t.Fatal("a preview changed the subscription")
		}
		r, err := e.plans.ChangeSubscription(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		sameQuote(t, r.GetQuote(), q)
		nu := r.GetSubscription()
		if r.GetScheduled() || nu.GetId() == old.GetId() || nu.GetPlanCode() != "pro" || nu.GetChangedFromId() != old.GetId() || nu.GetFeeTiming() != plansv1.FeeTiming_FEE_TIMING_ADVANCE {
			t.Fatalf("upgrade result = %v", r)
		}
		if g, _ := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: old.GetId()}); g.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED {
			t.Errorf("the old subscription is %v", g.GetSubscription().GetState())
		}
		// The customer's fee was settled in the period already invoiced: nothing is due again until it ends.
		due, _ := e.plans.ListSubscriptionsFeeDue(ctx, &plansv1.ListSubscriptionsFeeDueRequest{Now: timestamppb.Now()})
		for _, d := range due.GetDue() {
			if d.GetSubscription().GetId() == nu.GetId() || d.GetSubscription().GetId() == old.GetId() {
				t.Errorf("a fee is due right after the change: %v", d)
			}
		}
		adj, err := e.plans.ListSubscriptionAdjustments(ctx, &plansv1.ListSubscriptionAdjustmentsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var mine *plansv1.Adjustment
		for _, a := range adj.GetAdjustments() {
			if a.GetSubscription().GetId() == nu.GetId() {
				mine = a
			}
		}
		if mine == nil || mine.GetCreditMinor() != r.GetQuote().GetCreditMinor() || mine.GetChargeMinor() != r.GetQuote().GetChargeMinor() || mine.GetOldPlanCode() != "basic" {
			t.Fatalf("adjustment = %v", mine)
		}
		if _, err := e.plans.MarkSubscriptionAdjustmentBilled(ctx, &plansv1.MarkSubscriptionAdjustmentBilledRequest{AdjustmentId: mine.GetId()}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.MarkSubscriptionAdjustmentBilled(ctx, &plansv1.MarkSubscriptionAdjustmentBilledRequest{AdjustmentId: mine.GetId()}); err != nil {
			t.Fatalf("marking twice: %v", err)
		}
		adj, _ = e.plans.ListSubscriptionAdjustments(ctx, &plansv1.ListSubscriptionAdjustmentsRequest{})
		for _, a := range adj.GetAdjustments() {
			if a.GetId() == mine.GetId() {
				t.Error("a billed adjustment is still listed")
			}
		}
	})

	t.Run("a downgrade waits for the end of the period; it can be withdrawn; applying twice does nothing", func(t *testing.T) {
		old := subscribe("pro", month, 10, 0)
		r, err := e.plans.ChangeSubscription(ctx, change(old.GetId(), "basic", 0, 0, 0))
		if err != nil {
			t.Fatal(err)
		}
		if !r.GetScheduled() || r.GetSubscription().GetId() != old.GetId() || r.GetSubscription().GetScheduledChange().GetPlanCode() != "basic" ||
			r.GetQuote().GetNetMinor() != 0 || r.GetQuote().GetNextRenewalMinor() != 3000 {
			t.Fatalf("scheduled = %v", r)
		}
		effective := r.GetQuote().GetEffectiveTime().AsTime()
		if g, _ := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: old.GetId()}); g.GetSubscription().GetScheduledChange() == nil {
			t.Fatal("the scheduled change is not shown on the subscription")
		}
		if l, _ := e.plans.ListSubscriptions(ctx, &plansv1.ListSubscriptionsRequest{CustomerExternalId: old.GetCustomerExternalId()}); l.GetSubscriptions()[0].GetScheduledChange() == nil {
			t.Fatal("the scheduled change is not shown in the list")
		}
		// Withdrawn, nothing happens at the end of the period.
		c, err := e.plans.CancelScheduledChange(ctx, &plansv1.CancelScheduledChangeRequest{SubscriptionId: old.GetId()})
		if err != nil || c.GetSubscription().GetScheduledChange() != nil {
			t.Fatalf("withdraw = %v, %v", c, err)
		}
		if _, err := e.plans.CancelScheduledChange(ctx, &plansv1.CancelScheduledChangeRequest{SubscriptionId: old.GetId()}); code(t, err) != codes.FailedPrecondition {
			t.Fatalf("withdrawing nothing: %v", err)
		}
		// Scheduling again and again keeps one.
		for range 2 {
			if _, err := e.plans.ChangeSubscription(ctx, change(old.GetId(), "basic", 0, 0, 0)); err != nil {
				t.Fatal(err)
			}
		}
		ap := func(at time.Time) int32 {
			a, err := e.plans.ApplyScheduledChanges(ctx, &plansv1.ApplyScheduledChangesRequest{Now: timestamppb.New(at)})
			if err != nil {
				t.Fatal(err)
			}
			return a.GetApplied()
		}
		if got := ap(effective.Add(-time.Hour)); got != 0 {
			t.Fatalf("applied %d changes before the end of the period", got)
		}
		if got := ap(effective.Add(time.Second)); got < 1 {
			t.Fatalf("applied %d changes at the end of the period; want at least this one", got)
		}
		if got := ap(effective.Add(time.Second)); got != 0 {
			t.Fatalf("applied %d changes again", got)
		}
		l, _ := e.plans.ListSubscriptions(ctx, &plansv1.ListSubscriptionsRequest{CustomerExternalId: old.GetCustomerExternalId()})
		cur := l.GetSubscriptions()[0]
		if cur.GetPlanCode() != "basic" || cur.GetChangedFromId() != old.GetId() || !cur.GetStartTime().AsTime().Equal(effective) || cur.GetScheduledChange() != nil {
			t.Fatalf("after the change: %v", cur)
		}
		// The new plan's first fee is due at once (the period it opens has started).
		due, _ := e.plans.ListSubscriptionsFeeDue(ctx, &plansv1.ListSubscriptionsFeeDueRequest{Now: timestamppb.New(effective.Add(time.Second))})
		var sawNew, sawOld bool
		for _, d := range due.GetDue() {
			if d.GetSubscription().GetId() == cur.GetId() {
				sawNew = d.GetFeeMinor() == 3000 && d.GetPeriodStart().AsTime().Equal(effective)
			}
			if d.GetSubscription().GetId() == old.GetId() {
				sawOld = true
			}
		}
		if !sawNew || sawOld {
			t.Errorf("fee due: new %v, old %v; want true, false", sawNew, sawOld)
		}
	})

	t.Run("a downgrade made immediate turns the difference into credit", func(t *testing.T) {
		old := subscribe("pro", month, 10, 0)
		r, err := e.plans.ChangeSubscription(ctx, change(old.GetId(), "basic", 0, plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY, 0))
		if err != nil {
			t.Fatal(err)
		}
		if r.GetScheduled() || r.GetQuote().GetNetMinor() >= 0 || r.GetQuote().GetCreditMinor() <= r.GetQuote().GetChargeMinor() {
			t.Fatalf("quote = %v", r.GetQuote())
		}
	})

	t.Run("monthly to yearly starts the year now; yearly to monthly waits for the end of the year", func(t *testing.T) {
		old := subscribe("basic", month, 10, 0)
		r, err := e.plans.ChangeSubscription(ctx, change(old.GetId(), "", year, 0, 0))
		if err != nil {
			t.Fatal(err)
		}
		q, nu := r.GetQuote(), r.GetSubscription()
		if r.GetScheduled() || nu.GetInterval() != year || nu.GetPlanCode() != "basic" || q.GetChargeMinor() != 30000 || q.GetCreditMinor() <= 0 || q.GetNetMinor() != 30000-q.GetCreditMinor() {
			t.Fatalf("monthly to yearly = %v", r)
		}
		if want := nu.GetStartTime().AsTime().AddDate(1, 0, 0); !q.GetNextRenewalTime().AsTime().Equal(want) || q.GetNextRenewalMinor() != 30000 {
			t.Errorf("next renewal %v for %d; want %v for 30000", q.GetNextRenewalTime().AsTime(), q.GetNextRenewalMinor(), want)
		}
		back, err := e.plans.ChangeSubscription(ctx, change(nu.GetId(), "", month, 0, 0))
		if err != nil {
			t.Fatal(err)
		}
		if !back.GetScheduled() || back.GetQuote().GetNetMinor() != 0 || !back.GetQuote().GetEffectiveTime().AsTime().Equal(nu.GetStartTime().AsTime().AddDate(1, 0, 0)) {
			t.Fatalf("yearly to monthly = %v", back)
		}
		if _, err := e.plans.ChangeSubscription(ctx, change(nu.GetId(), "", month, 0, plansv1.ChangeProration_CHANGE_PRORATION_NONE)); err != nil {
			t.Fatalf("replacing the scheduled change: %v", err)
		}
	})

	t.Run("during a trial the plan changes with no money", func(t *testing.T) {
		old := subscribe("basic", month, 1, 14)
		r, err := e.plans.ChangeSubscription(ctx, change(old.GetId(), "pro", 0, 0, 0))
		if err != nil {
			t.Fatal(err)
		}
		nu := r.GetSubscription()
		if r.GetScheduled() || nu.GetPlanCode() != "pro" || nu.GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING ||
			!nu.GetTrialEndTime().AsTime().Equal(old.GetTrialEndTime().AsTime()) || r.GetQuote().GetNetMinor() != 0 || r.GetQuote().GetNextRenewalMinor() != 9000 {
			t.Fatalf("trial change = %v", r)
		}
		if _, err := e.plans.ChangeSubscription(ctx, change(nu.GetId(), "basic", 0, plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END, 0)); code(t, err) != codes.InvalidArgument {
			t.Errorf("scheduling a change during a trial: %v", err)
		}
		due, _ := e.plans.ListSubscriptionsFeeDue(ctx, &plansv1.ListSubscriptionsFeeDueRequest{Now: timestamppb.New(nu.GetTrialEndTime().AsTime().Add(time.Second))})
		for _, d := range due.GetDue() {
			if d.GetSubscription().GetId() == nu.GetId() && (d.GetFeeMinor() != 9000 || !d.GetPeriodStart().AsTime().Equal(nu.GetTrialEndTime().AsTime())) {
				t.Errorf("the first fee after the trial = %v; want the new plan's 9000 from the trial end", d)
			}
		}
	})

	t.Run("a subscription a provider runs follows the plan and interval the provider bills, with no money", func(t *testing.T) {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_paddle_follow", Name: "p", Email: "p@example.com", Country: "US", BillingCurrency: "USD"}); err != nil {
			t.Fatal(err)
		}
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_paddle_follow", PlanCode: "basic", Provider: "paddle"})
		if err != nil {
			t.Fatal(err)
		}
		first := r.GetSubscription()
		at := time.Now().Add(-time.Hour)
		if _, err := e.plans.SyncProviderSubscription(ctx, &plansv1.SyncProviderSubscriptionRequest{SubscriptionId: first.GetId(), Provider: "paddle", ProviderRef: "sub_follow",
			Owner: "provider", ProviderStatus: "active", OccurredAt: timestamppb.New(at)}); err != nil {
			t.Fatal(err)
		}
		pro, err := e.plans.GetPlanVersion(ctx, &plansv1.GetPlanVersionRequest{PlanCode: "pro"})
		if err != nil {
			t.Fatal(err)
		}
		sync := func(occurred time.Time, status string, pv string, iv plansv1.SubscriptionInterval) *plansv1.Subscription {
			t.Helper()
			resp, err := e.plans.SyncProviderSubscription(ctx, &plansv1.SyncProviderSubscriptionRequest{SubscriptionId: first.GetId(), Provider: "paddle", ProviderRef: "sub_follow",
				Owner: "provider", ProviderStatus: status, OccurredAt: timestamppb.New(occurred), PlanVersionId: pv, Interval: iv})
			if err != nil {
				t.Fatal(err)
			}
			return resp.GetSubscription()
		}
		now := time.Now()
		got := sync(now.Add(-time.Minute), "active", pro.GetPlanVersion().GetId(), 0)
		if got.GetId() == first.GetId() || got.GetPlanCode() != "pro" || got.GetOwner() != "provider" || got.GetProviderRef() != "sub_follow" || got.GetChangedFromId() != first.GetId() ||
			got.GetFeeTiming() != plansv1.FeeTiming_FEE_TIMING_ARREARS {
			t.Fatalf("after the provider moved the plan: %v", got)
		}
		if old, _ := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: first.GetId()}); old.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED {
			t.Errorf("the old subscription is %v", old.GetSubscription().GetState())
		}
		// The same event again changes nothing; it is not a second switch.
		again := sync(now.Add(-time.Minute), "active", pro.GetPlanVersion().GetId(), 0)
		if again.GetId() != got.GetId() {
			t.Errorf("a repeated event made another subscription: %s then %s", got.GetId(), again.GetId())
		}
		// Nor does money move, and nothing is invoiced in advance for a provider-run subscription.
		adj, _ := e.plans.ListSubscriptionAdjustments(ctx, &plansv1.ListSubscriptionAdjustmentsRequest{PageSize: 500})
		for _, a := range adj.GetAdjustments() {
			if a.GetSubscription().GetId() == got.GetId() {
				t.Errorf("an adjustment for a provider-run change: %v", a)
			}
		}
	})

	t.Run("refusals", func(t *testing.T) {
		sub := subscribe("basic", month, 10, 0)
		for name, tc := range map[string]struct {
			req  *plansv1.ChangeSubscriptionRequest
			want codes.Code
		}{
			"the same plan":                     {change(sub.GetId(), "basic", 0, 0, 0), codes.InvalidArgument},
			"an unknown plan":                   {change(sub.GetId(), "nope", 0, 0, 0), codes.NotFound},
			"no proration across an interval":   {change(sub.GetId(), "", year, 0, plansv1.ChangeProration_CHANGE_PRORATION_NONE), codes.InvalidArgument},
			"proration for a change at the end": {change(sub.GetId(), "pro", 0, plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END, plansv1.ChangeProration_CHANGE_PRORATION_PRORATE), codes.InvalidArgument},
			"not a uuid":                        {change("x", "pro", 0, 0, 0), codes.InvalidArgument},
		} {
			if _, err := e.plans.ChangeSubscription(ctx, tc.req); code(t, err) != tc.want {
				t.Errorf("%s: %v; want %v", name, err, tc.want)
			}
			if _, err := e.plans.PreviewSubscriptionChange(ctx, &plansv1.PreviewSubscriptionChangeRequest{Change: tc.req}); code(t, err) != tc.want {
				t.Errorf("preview, %s: %v; want %v", name, err, tc.want)
			}
		}
		// A fee that has not been invoiced yet is no fee to credit.
		n++
		ext := "cus_chg_fresh"
		_, _ = e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"})
		fresh, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "basic", StartTime: timestamppb.New(time.Now().Add(-time.Hour))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.ChangeSubscription(ctx, change(fresh.GetSubscription().GetId(), "pro", 0, 0, 0)); code(t, err) != codes.Aborted {
			t.Errorf("changing before the first fee is invoiced: %v", err)
		}
		// A subscription billed in arrears is changed with ChangePlan.
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_chg_arrears", Name: "a", Email: "a@example.com"}); err != nil {
			t.Fatal(err)
		}
		arr, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_chg_arrears", PlanCode: "basic", FeeTiming: plansv1.FeeTiming_FEE_TIMING_ARREARS})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.ChangeSubscription(ctx, change(arr.GetSubscription().GetId(), "pro", 0, 0, 0)); code(t, err) != codes.FailedPrecondition {
			t.Errorf("changing an arrears subscription: %v", err)
		}
	})
}
