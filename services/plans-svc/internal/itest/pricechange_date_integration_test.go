//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// Plan 15, leftovers. A price change may name the date its new price starts from: each subscription then moves on its first period start on or after the date, so a
// monthly one renews at the old price until then. The notice is the run's, or the operator's minimum for the customer's country when that is longer. Both rules sit
// in the operator's table, which only the operator edits.
func testPriceChangeDateAndNotice(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Price date")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	for _, fee := range []int64{3000, 4000} {
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: fee}); err != nil {
			t.Fatal(err)
		}
	}
	// The table is the platform's: the tests that follow share it.
	defer func() {
		_, _ = e.admin.SetPriceChangeNotice(operator(), &plansv1.SetPriceChangeNoticeRequest{DefaultNoticeDays: 30, Rules: []*plansv1.PriceChangeNoticeRule{{Country: "*", MinNoticeDays: 14}}})
	}()
	subscribe := func(ext, country string, startedDaysAgo int) string {
		t.Helper()
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com", Country: country}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-time.Duration(startedDaysAgo) * 24 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "basic", PlanVersion: 1, StartTime: timestamppb.New(start)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
			t.Fatal(err)
		}
		return r.GetSubscription().GetId()
	}
	run := func(req *plansv1.SchedulePriceChangeRequest) (*plansv1.SchedulePriceChangeResponse, error) {
		req.PlanCode, req.FromVersion = "basic", 1
		return e.plans.SchedulePriceChange(ctx, req)
	}
	day := 24 * time.Hour
	setNotice := func(def int32, rules map[string]int32) {
		t.Helper()
		req := &plansv1.SetPriceChangeNoticeRequest{DefaultNoticeDays: def}
		for c, d := range rules {
			req.Rules = append(req.Rules, &plansv1.PriceChangeNoticeRule{Country: c, MinNoticeDays: d})
		}
		if _, err := e.admin.SetPriceChangeNotice(operator(), req); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("the operator's table: only the operator edits it, rows are checked, a replace is whole", func(t *testing.T) {
		got, err := e.admin.GetPriceChangeNotice(operator(), &plansv1.GetPriceChangeNoticeRequest{})
		if err != nil || got.GetNotice().GetDefaultNoticeDays() != 30 || len(got.GetNotice().GetRules()) != 1 || got.GetNotice().GetRules()[0].GetCountry() != "*" || got.GetNotice().GetRules()[0].GetMinNoticeDays() != 14 {
			t.Fatalf("seeded: %v, %v", got, err)
		}
		rule := func(c string, d int32) *plansv1.PriceChangeNoticeRule {
			return &plansv1.PriceChangeNoticeRule{Country: c, MinNoticeDays: d}
		}
		_, err = e.admin.SetPriceChangeNotice(ctx, &plansv1.SetPriceChangeNoticeRequest{DefaultNoticeDays: 30})
		wantCode(t, "a tenant setting the table", err, codes.PermissionDenied, "")
		for what, req := range map[string]*plansv1.SetPriceChangeNoticeRequest{
			"a default of 7 days":       {DefaultNoticeDays: 7},
			"a country row of 7 days":   {DefaultNoticeDays: 30, Rules: []*plansv1.PriceChangeNoticeRule{rule("TR", 7)}},
			"a country that is no code": {DefaultNoticeDays: 30, Rules: []*plansv1.PriceChangeNoticeRule{rule("Turkey", 30)}},
			"the same country twice":    {DefaultNoticeDays: 30, Rules: []*plansv1.PriceChangeNoticeRule{rule("TR", 30), rule("tr", 40)}},
			"a row of more than a year": {DefaultNoticeDays: 30, Rules: []*plansv1.PriceChangeNoticeRule{rule("TR", 366)}},
			"a note of 201 characters":  {DefaultNoticeDays: 30, Rules: []*plansv1.PriceChangeNoticeRule{{Country: "TR", MinNoticeDays: 30, Note: string(make([]rune, 201))}}},
		} {
			_, err := e.admin.SetPriceChangeNotice(operator(), req)
			wantCode(t, what, err, codes.InvalidArgument, "plans.request.invalid")
		}
		set, err := e.admin.SetPriceChangeNotice(operator(), &plansv1.SetPriceChangeNoticeRequest{DefaultNoticeDays: 45, Rules: []*plansv1.PriceChangeNoticeRule{rule("de", 60), rule("*", 21)}, Actor: "ops@example.com"})
		if err != nil || set.GetNotice().GetDefaultNoticeDays() != 45 || set.GetNotice().GetUpdatedBy() != "ops@example.com" || set.GetNotice().GetUpdateTime() == nil {
			t.Fatalf("set: %v, %v", set, err)
		}
		if r := set.GetNotice().GetRules(); len(r) != 2 || r[0].GetCountry() != "*" || r[1].GetCountry() != "DE" || r[1].GetMinNoticeDays() != 60 {
			t.Fatalf("rules, the star first and countries in capitals: %v", r)
		}
		// A run that names no notice uses the table's default, and records it.
		r, err := run(&plansv1.SchedulePriceChangeRequest{DryRun: true})
		if err != nil || r.GetNoticeDays() != 45 {
			t.Fatalf("a dry run with no notice: %v, %v", r, err)
		}
		if _, err := e.admin.SetPriceChangeNotice(operator(), &plansv1.SetPriceChangeNoticeRequest{DefaultNoticeDays: 30}); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.admin.GetPriceChangeNotice(operator(), &plansv1.GetPriceChangeNoticeRequest{}); len(got.GetNotice().GetRules()) != 0 {
			t.Errorf("a replace that lists nothing leaves rows: %v", got)
		}
	})

	// Two monthly subscriptions that renew in about 25 days, one in Germany and one in Turkey.
	de := subscribe("cus_de", "DE", 5)
	tr := subscribe("cus_tr", "TR", 5)
	setNotice(30, map[string]int32{"DE": 60})

	t.Run("a customer in a country with a longer minimum is skipped as renewing too soon, the others are not", func(t *testing.T) {
		r, err := run(&plansv1.SchedulePriceChangeRequest{NoticeDays: 14, DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		if r.GetScheduled() != 1 || r.GetScheduledSubscriptionIds()[0] != tr || len(r.GetSkipped()) != 1 || r.GetSkipped()[0].GetSubscriptionId() != de || r.GetSkipped()[0].GetReason() != "renews_soon" {
			t.Fatalf("with a 60-day minimum for Germany: %v", r)
		}
		// Without the row both go through: the test fails without the table.
		setNotice(30, nil)
		if r, err := run(&plansv1.SchedulePriceChangeRequest{NoticeDays: 14, DryRun: true}); err != nil || r.GetScheduled() != 2 {
			t.Fatalf("with no country row: %v, %v", r, err)
		}
	})

	t.Run("a date: each subscription moves on its first renewal on or after it, and the date is bounded", func(t *testing.T) {
		now := time.Now()
		_, err := run(&plansv1.SchedulePriceChangeRequest{NoticeDays: 30, EffectiveFrom: timestamppb.New(now.Add(10 * day))})
		wantCode(t, "a date inside the notice", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = run(&plansv1.SchedulePriceChangeRequest{NoticeDays: 30, EffectiveFrom: timestamppb.New(now.Add(800 * day))})
		wantCode(t, "a date more than two years away", err, codes.InvalidArgument, "plans.request.invalid")
		from := now.Add(100 * day).Truncate(time.Microsecond)
		r, err := run(&plansv1.SchedulePriceChangeRequest{NoticeDays: 30, EffectiveFrom: timestamppb.New(from), Actor: "boss@example.com"})
		if err != nil || r.GetScheduled() != 2 || r.GetPriceChangeId() == "" {
			t.Fatalf("a dated run: %v, %v", r, err)
		}
		g, err := e.plans.GetPriceChange(ctx, &plansv1.GetPriceChangeRequest{Id: r.GetPriceChangeId()})
		if err != nil || !g.GetPriceChange().GetEffectiveFrom().AsTime().Equal(from) {
			t.Fatalf("the record keeps the date: %v, %v", g, err)
		}
		// Started 5 days ago, renewing monthly: the renewals are about 25, 56, 86, 117 days from now. The first on or after day 100 is the one about 117 days away,
		// not the next one (about 25 days) the old model would have used.
		for _, it := range g.GetItems() {
			eff := it.GetEffectiveTime().AsTime()
			if eff.Before(from) || eff.Sub(from) >= 32*day {
				t.Errorf("%s moves on %v: not the first renewal on or after %v", it.GetCustomerExternalId(), eff, from)
			}
		}
		if !g.GetPriceChange().GetEffectiveTime().AsTime().Equal(g.GetItems()[0].GetEffectiveTime().AsTime()) {
			t.Errorf("the record's first date %v is not the first item's %v", g.GetPriceChange().GetEffectiveTime(), g.GetItems()[0].GetEffectiveTime())
		}
		// The change is scheduled for that date, so the renewals before it are the old subscription's: nothing is applied now.
		if n, err := e.plans.ApplyScheduledChanges(ctx, &plansv1.ApplyScheduledChangesRequest{Now: timestamppb.New(now.Add(60 * day))}); err != nil || n.GetApplied() != 0 {
			t.Errorf("applied 60 days from now, before the date: %v, %v", n, err)
		}

		t.Run("running it again must name the same date", func(t *testing.T) {
			_, err := run(&plansv1.SchedulePriceChangeRequest{NoticeDays: 30, EffectiveFrom: timestamppb.New(from.Add(10 * day))})
			wantCode(t, "another date", err, codes.FailedPrecondition, "plans.price_change.date_differs")
			_, err = run(&plansv1.SchedulePriceChangeRequest{NoticeDays: 30})
			wantCode(t, "no date for a dated price change", err, codes.FailedPrecondition, "plans.price_change.date_differs")
			again, err := run(&plansv1.SchedulePriceChangeRequest{NoticeDays: 30, EffectiveFrom: timestamppb.New(from)})
			if err != nil || again.GetAlreadyScheduled() != 2 || again.GetScheduled() != 0 || again.GetPriceChangeId() != r.GetPriceChangeId() {
				t.Fatalf("same date: %v, %v", again, err)
			}
			// A retry carries the date of the price change itself.
			if rr, err := e.plans.RetryPriceChange(ctx, &plansv1.RetryPriceChangeRequest{Id: r.GetPriceChangeId()}); err != nil || rr.GetResult().GetAlreadyScheduled() != 2 {
				t.Fatalf("retry: %v, %v", rr, err)
			}
		})

		t.Run("the change applies on its date and the subscription is on version 2 from then", func(t *testing.T) {
			late := g.GetItems()[0].GetEffectiveTime().AsTime().Add(time.Minute)
			if n, err := e.plans.ApplyScheduledChanges(ctx, &plansv1.ApplyScheduledChangesRequest{Now: timestamppb.New(late)}); err != nil || n.GetApplied() != 2 {
				t.Fatalf("applied on the date: %v, %v", n, err)
			}
		})
	})
}
