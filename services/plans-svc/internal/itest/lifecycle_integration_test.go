//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

func testSubscriptionLifecycle(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Lifecycle")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	for _, c := range []struct {
		code string
		fee  int64
	}{{"basic", 3000}, {"pro", 9000}} {
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: c.code, Name: c.code}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: c.code, BaseFeeMinor: c.fee,
			Fees: []*plansv1.CurrencyFee{{Currency: "USD", BaseFeeMinor: c.fee / 30}}}); err != nil {
			t.Fatal(err)
		}
	}
	mkCustomer := func(id string) {
		t.Helper()
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: id, Name: id, Email: id + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("trial then active, pause and resume", func(t *testing.T) {
		mkCustomer("cus_trial")
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_trial", PlanCode: "basic", TrialDays: 14})
		if err != nil {
			t.Fatal(err)
		}
		sub := r.GetSubscription()
		if sub.GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING || sub.GetTrialEndTime() == nil || sub.GetCurrency() != "TRY" {
			t.Fatalf("new trial = %v", sub)
		}
		if code(t, pauseErr(e, ctx, sub.GetId())) != codes.FailedPrecondition {
			t.Fatal("a trialing subscription cannot be paused")
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_trial", PlanCode: "basic", TrialDays: 400}); code(t, err) != codes.InvalidArgument {
			t.Fatalf("trial_days 400: %v", err)
		}

		mkCustomer("cus_pause")
		r, err = e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_pause", PlanCode: "basic"})
		if err != nil || r.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE {
			t.Fatalf("active = %v, %v", r, err)
		}
		id := r.GetSubscription().GetId()
		p, err := e.plans.PauseSubscription(ctx, &plansv1.PauseSubscriptionRequest{SubscriptionId: id})
		if err != nil || p.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED || p.GetSubscription().GetPauseTime() == nil {
			t.Fatalf("pause = %v, %v", p, err)
		}
		if code(t, pauseErr(e, ctx, id)) != codes.FailedPrecondition {
			t.Fatal("pausing a paused subscription must fail")
		}
		rs, err := e.plans.ResumeSubscription(ctx, &plansv1.ResumeSubscriptionRequest{SubscriptionId: id})
		if err != nil || rs.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE || rs.GetSubscription().GetPauseTime() != nil {
			t.Fatalf("resume = %v, %v", rs, err)
		}
		if _, err := e.plans.ResumeSubscription(ctx, &plansv1.ResumeSubscriptionRequest{SubscriptionId: id}); code(t, err) != codes.FailedPrecondition {
			t.Fatalf("resume of an active subscription: %v", err)
		}
	})

	t.Run("cancel at period end can be withdrawn", func(t *testing.T) {
		mkCustomer("cus_sched")
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_sched", PlanCode: "basic"})
		if err != nil {
			t.Fatal(err)
		}
		id := r.GetSubscription().GetId()
		sc, err := e.plans.ScheduleCancellation(ctx, &plansv1.ScheduleCancellationRequest{SubscriptionId: id, Cancel: true})
		if err != nil {
			t.Fatal(err)
		}
		got := sc.GetSubscription()
		if !got.GetCancelAtPeriodEnd() || got.GetEndTime() == nil || !got.GetEndTime().AsTime().After(time.Now()) ||
			got.GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE {
			t.Fatalf("scheduled = %v", got)
		}
		// The period is one month from the start, so the end is within 32 days.
		if got.GetEndTime().AsTime().After(time.Now().Add(32 * 24 * time.Hour)) {
			t.Fatalf("end %v is not within one month", got.GetEndTime().AsTime())
		}
		if _, err := e.plans.ScheduleCancellation(ctx, &plansv1.ScheduleCancellationRequest{SubscriptionId: id, Cancel: true}); code(t, err) != codes.FailedPrecondition {
			t.Fatalf("scheduling twice: %v", err)
		}
		w, err := e.plans.ScheduleCancellation(ctx, &plansv1.ScheduleCancellationRequest{SubscriptionId: id, Cancel: false})
		if err != nil || w.GetSubscription().GetEndTime() != nil || w.GetSubscription().GetCancelAtPeriodEnd() {
			t.Fatalf("withdraw = %v, %v", w, err)
		}
		if _, err := e.plans.ScheduleCancellation(ctx, &plansv1.ScheduleCancellationRequest{SubscriptionId: id, Cancel: false}); code(t, err) != codes.FailedPrecondition {
			t.Fatalf("withdrawing nothing: %v", err)
		}
	})

	t.Run("a plan change splits the month's fixed fee between the plans and chains the subscriptions", func(t *testing.T) {
		mkCustomer("cus_change")
		start := time.Now().Add(-24 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_change", PlanCode: "basic", StartTime: timestamppb.New(start)})
		if err != nil {
			t.Fatal(err)
		}
		old := r.GetSubscription()
		at := time.Now().Truncate(time.Microsecond)
		cp, err := e.plans.ChangePlan(ctx, &plansv1.ChangePlanRequest{SubscriptionId: old.GetId(), PlanCode: "pro", ChangeTime: timestamppb.New(at)})
		if err != nil {
			t.Fatal(err)
		}
		// basic costs 3000 and pro 9000 a month, so the same share of a month is three times as dear on pro.
		if cp.GetOldPlanFeeMinor() < 0 || cp.GetOldPlanFeeMinor() > 3000 || cp.GetNewPlanFeeMinor() < 0 || cp.GetNewPlanFeeMinor() > 9000 {
			t.Fatalf("shares out of range: old %d new %d", cp.GetOldPlanFeeMinor(), cp.GetNewPlanFeeMinor())
		}
		if cp.GetOldPlanFeeMinor() == 0 && cp.GetNewPlanFeeMinor() == 0 {
			t.Fatal("a change inside the month must charge something")
		}
		oldNow, err := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: old.GetId()})
		if err != nil || oldNow.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED ||
			!oldNow.GetSubscription().GetEndTime().AsTime().Equal(at) {
			t.Fatalf("old subscription = %v, %v", oldNow, err)
		}
		nw := cp.GetSubscription()
		if !nw.GetStartTime().AsTime().Equal(at) || nw.GetEndTime() != nil || nw.GetPlanVersionId() == old.GetPlanVersionId() ||
			nw.GetChangedFromId() != old.GetId() {
			t.Fatalf("new subscription = %v", nw)
		}
		if _, err := e.plans.ChangePlan(ctx, &plansv1.ChangePlanRequest{SubscriptionId: nw.GetId(), PlanCode: "pro"}); code(t, err) != codes.InvalidArgument {
			t.Fatalf("same plan: %v", err)
		}
		if _, err := e.plans.ChangePlan(ctx, &plansv1.ChangePlanRequest{SubscriptionId: old.GetId(), PlanCode: "pro"}); code(t, err) != codes.FailedPrecondition {
			t.Fatalf("changing an ended subscription: %v", err)
		}
	})

	t.Run("a subscription a provider runs is linked, followed and not changed locally", func(t *testing.T) {
		for _, id := range []string{"cus_paddle", "cus_paddle2"} {
			if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: id, Name: id, Email: id + "@example.com", Country: "DE", BillingCurrency: "USD"}); err != nil {
				t.Fatal(err)
			}
		}
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_paddle", PlanCode: "basic", Provider: "paddle"})
		if err != nil || r.GetSubscription().GetProvider() != "paddle" || r.GetSubscription().GetOwner() != "quotify" || r.GetSubscription().GetProviderRef() != "" {
			t.Fatalf("created = %v, %v; it names its provider but is not linked or provider-owned yet", r, err)
		}
		id := r.GetSubscription().GetId()
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_paddle", PlanCode: "basic", Provider: "Not A Name"}); code(t, err) != codes.InvalidArgument {
			t.Fatalf("bad provider name: %v", err)
		}
		t0 := time.Now().Add(-time.Hour)
		link := func(ref, owner, status string, at time.Time) (*plansv1.SyncProviderSubscriptionResponse, error) {
			return e.plans.SyncProviderSubscription(ctx, &plansv1.SyncProviderSubscriptionRequest{SubscriptionId: id, Provider: "paddle", ProviderRef: ref,
				Owner: owner, ProviderStatus: status, OccurredAt: timestamppb.New(at)})
		}
		got, err := link("sub_01", "provider", "active", t0)
		sub := got.GetSubscription()
		if err != nil || sub.GetOwner() != "provider" || sub.GetProviderRef() != "sub_01" || sub.GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE {
			t.Fatalf("link = %v, %v", got, err)
		}
		if g, err := link("sub_01", "provider", "past_due", t0.Add(time.Minute)); err != nil || g.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE {
			t.Fatalf("past due = %v, %v", g, err)
		}
		// An older event arriving late changes nothing.
		if g, err := link("sub_01", "provider", "active", t0.Add(30*time.Second)); err != nil || g.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE {
			t.Fatalf("stale event = %v, %v", g, err)
		}
		if _, err := link("sub_other", "provider", "active", t0.Add(time.Hour)); code(t, err) != codes.FailedPrecondition {
			t.Fatalf("relinking to another provider subscription: %v", err)
		}
		if _, err := link("sub_01", "provider", "bogus", t0.Add(time.Hour)); code(t, err) != codes.InvalidArgument {
			t.Fatalf("bad status: %v", err)
		}
		// Nothing changes it locally.
		if _, err := e.plans.PauseSubscription(ctx, &plansv1.PauseSubscriptionRequest{SubscriptionId: id}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("pause: %v", err)
		}
		if _, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: id}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("cancel: %v", err)
		}
		if _, err := e.plans.ChangePlan(ctx, &plansv1.ChangePlanRequest{SubscriptionId: id, PlanCode: "pro"}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("change plan: %v", err)
		}
		if _, err := e.plans.ScheduleCancellation(ctx, &plansv1.ScheduleCancellationRequest{SubscriptionId: id, Cancel: true}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("schedule cancellation: %v", err)
		}
		// The provider cancels: the subscription ends.
		if g, err := link("sub_01", "provider", "canceled", t0.Add(2*time.Hour)); err != nil ||
			g.GetSubscription().GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED || g.GetSubscription().GetEndTime() == nil {
			t.Fatalf("canceled = %v, %v", g, err)
		}
		// Another subscription cannot take the same provider id.
		r2, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_paddle2", PlanCode: "basic", Provider: "paddle"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.plans.SyncProviderSubscription(ctx, &plansv1.SyncProviderSubscriptionRequest{SubscriptionId: r2.GetSubscription().GetId(), Provider: "paddle",
			ProviderRef: "sub_01", Owner: "provider", ProviderStatus: "active"})
		if err == nil {
			t.Fatal("two subscriptions linked to the same provider subscription")
		}
	})

	t.Run("routing rules choose the provider from country and currency", func(t *testing.T) {
		rules := []*plansv1.RoutingRule{
			{Country: "TR", Currency: "TRY", Provider: "iyzico"},
			{Currency: "USD", Provider: "paddle"},
		}
		if _, err := e.plans.SetRoutingRules(ctx, &plansv1.SetRoutingRulesRequest{Rules: []*plansv1.RoutingRule{{Currency: "TRY", Provider: "paddle"}}}); code(t, err) != codes.InvalidArgument {
			t.Fatalf("paddle cannot charge TRY: %v", err)
		}
		if _, err := e.plans.SetRoutingRules(ctx, &plansv1.SetRoutingRulesRequest{Rules: []*plansv1.RoutingRule{{Provider: "stripe"}}}); code(t, err) != codes.InvalidArgument {
			t.Fatalf("unknown provider: %v", err)
		}
		got, err := e.plans.GetRoutingRules(ctx, &plansv1.GetRoutingRulesRequest{})
		if err != nil || len(got.GetRules()) != 0 {
			t.Fatalf("a refused list changed nothing: %v, %v", got, err)
		}
		set, err := e.plans.SetRoutingRules(ctx, &plansv1.SetRoutingRulesRequest{Rules: rules})
		if err != nil || len(set.GetRules()) != 2 {
			t.Fatalf("set = %v, %v", set, err)
		}
		if got, _ = e.plans.GetRoutingRules(ctx, &plansv1.GetRoutingRulesRequest{}); len(got.GetRules()) != 2 || got.GetRules()[0].GetProvider() != "iyzico" || got.GetRules()[1].GetCountry() != "" {
			t.Fatalf("get = %v", got)
		}
		for _, c := range []struct {
			country, currency, provider, owner string
			index                              int32
			can                                bool
		}{
			{"TR", "TRY", "iyzico", "quotify", 0, true},
			{"DE", "USD", "paddle", "provider", 1, true},
			{"TR", "EUR", "", "", -1, true},
		} {
			p, err := e.plans.PreviewRouting(ctx, &plansv1.PreviewRoutingRequest{Country: c.country, Currency: c.currency})
			if err != nil || p.GetProvider() != c.provider || p.GetOwner() != c.owner || p.GetRuleIndex() != c.index || p.GetCanCharge() != c.can {
				t.Errorf("preview %s/%s = %v, %v", c.country, c.currency, p, err)
			}
		}

		// A new subscription follows the rules; an explicit provider overrides them; the customer's currency decides which rule applies.
		mk := func(id, country, currency string) {
			if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: id, Name: id, Email: id + "@example.com", Country: country, BillingCurrency: currency}); err != nil {
				t.Fatal(err)
			}
		}
		mk("cus_tr", "TR", "TRY")
		mk("cus_us", "US", "USD")
		mk("cus_eur", "FR", "")
		sub := func(customer, provider string) *plansv1.Subscription {
			t.Helper()
			r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer, PlanCode: "basic", Provider: provider})
			if err != nil {
				t.Fatalf("%s: %v", customer, err)
			}
			return r.GetSubscription()
		}
		trSub := sub("cus_tr", "")
		if trSub.GetProvider() != "iyzico" {
			t.Errorf("Turkish customer in lira went to %q", trSub.GetProvider())
		}
		if s := sub("cus_us", ""); s.GetProvider() != "paddle" {
			t.Errorf("customer in dollars went to %q", s.GetProvider())
		}
		if s := sub("cus_eur", ""); s.GetProvider() != "" {
			t.Errorf("a customer no rule matches went to %q; it uses the default account", s.GetProvider())
		}
		// Editing the rules does not move existing subscriptions.
		if _, err := e.plans.SetRoutingRules(ctx, &plansv1.SetRoutingRulesRequest{}); err != nil {
			t.Fatal(err)
		}
		after, _ := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: trSub.GetId()})
		if after.GetSubscription().GetProvider() != "iyzico" {
			t.Errorf("an existing subscription moved when the rules changed: %v", after)
		}
		// An explicit provider overrides the rules (there are none now).
		mk("cus_explicit", "TR", "TRY")
		if s := sub("cus_explicit", "iyzico"); s.GetProvider() != "iyzico" {
			t.Errorf("explicit provider = %q", s.GetProvider())
		}
		// A provider that cannot charge the subscription's currency is refused at creation.
		mk("cus_tr2", "TR", "TRY")
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_tr2", PlanCode: "basic", Provider: "paddle"}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("paddle for a lira subscription: %v", err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_tr2", PlanCode: "basic", Provider: "stripe"}); code(t, err) != codes.InvalidArgument {
			t.Errorf("unknown provider: %v", err)
		}
	})

	t.Run("yearly subscriptions are refused for now", func(t *testing.T) {
		mkCustomer("cus_year")
		_, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_year", PlanCode: "basic",
			Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR})
		if code(t, err) != codes.Unimplemented {
			t.Fatalf("yearly: %v", err)
		}
	})

	t.Run("billing periods are the calendar months of the tenant", func(t *testing.T) {
		mkCustomer("cus_anchor")
		// 31 January 23:30 in Istanbul is still January there, though already February in UTC.
		loc, _ := time.LoadLocation("Europe/Istanbul")
		start := time.Date(2027, 1, 31, 23, 30, 0, 0, loc)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_anchor", PlanCode: "basic", StartTime: timestamppb.New(start)})
		if err != nil {
			t.Fatal(err)
		}
		sc, err := e.plans.ScheduleCancellation(ctx, &plansv1.ScheduleCancellationRequest{SubscriptionId: r.GetSubscription().GetId(), Cancel: true})
		if err != nil {
			t.Fatal(err)
		}
		// It has not started yet, so it ends with its first period: midnight on 1 February in Istanbul.
		if want := time.Date(2027, 2, 1, 0, 0, 0, 0, loc); !sc.GetSubscription().GetEndTime().AsTime().Equal(want) {
			t.Fatalf("cancel-at-period-end ends %s; want %s", sc.GetSubscription().GetEndTime().AsTime().In(loc), want)
		}
	})
}

func pauseErr(e *env, ctx context.Context, id string) error {
	_, err := e.plans.PauseSubscription(ctx, &plansv1.PauseSubscriptionRequest{SubscriptionId: id})
	return err
}

func testCurrencyFeesAndCustomerBilling(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Currencies")[plansv1.Environment_ENVIRONMENT_LIVE] // tenant currency TRY
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "team", Name: "Team"}); err != nil {
		t.Fatal(err)
	}

	for name, fees := range map[string][]*plansv1.CurrencyFee{
		"the tenant's own currency": {{Currency: "TRY", BaseFeeMinor: 1}},
		"a duplicate":               {{Currency: "USD", BaseFeeMinor: 1}, {Currency: "USD", BaseFeeMinor: 2}},
		"an unsupported currency":   {{Currency: "XXX", BaseFeeMinor: 1}},
		"a negative fee":            {{Currency: "USD", BaseFeeMinor: -1}},
	} {
		_, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "team", BaseFeeMinor: 5000, Fees: fees})
		if code(t, err) != codes.InvalidArgument {
			t.Errorf("fees with %s: %v", name, err)
		}
	}
	pv, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "team", BaseFeeMinor: 5000,
		Fees: []*plansv1.CurrencyFee{{Currency: "EUR", BaseFeeMinor: 400}, {Currency: "USD", BaseFeeMinor: 500}}})
	if err != nil || len(pv.GetPlanVersion().GetFees()) != 2 || pv.GetPlanVersion().GetFees()[0].GetCurrency() != "EUR" {
		t.Fatalf("publish with fees = %v, %v", pv, err)
	}

	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "bad", Name: "x", Email: "x@example.com", Country: "turkey"}); code(t, err) != codes.InvalidArgument {
		t.Fatalf("bad country: %v", err)
	}
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "bad", Name: "x", Email: "x@example.com", BillingCurrency: "ZZZ"}); code(t, err) != codes.InvalidArgument {
		t.Fatalf("bad billing currency: %v", err)
	}
	c, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "de", Name: "Hans", Email: "hans@example.com", Country: "DE", BillingCurrency: "EUR"})
	if err != nil || c.GetCustomer().GetCountry() != "DE" || c.GetCustomer().GetBillingCurrency() != "EUR" {
		t.Fatalf("customer = %v, %v", c, err)
	}

	// Usage prices must cover the base meters and a currency with prices needs a fee.
	if _, err := e.plans.CreateMeter(ctx, &plansv1.CreateMeterRequest{Code: "calls", Aggregation: plansv1.Aggregation_AGGREGATION_COUNT, QuantityScale: 0, UnitLabel: "calls"}); err != nil {
		t.Fatal(err)
	}
	perUnit := func(price string) *plansv1.Price {
		return &plansv1.Price{MeterCode: "calls", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: price}}}
	}
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "metered", Name: "Metered"}); err != nil {
		t.Fatal(err)
	}
	for name, cps := range map[string][]*plansv1.CurrencyPrices{
		"a currency without a fee":      {{Currency: "GBP", Prices: []*plansv1.Price{perUnit("1")}}},
		"a missing meter":               {{Currency: "USD"}},
		"a meter the plan does not use": {{Currency: "USD", Prices: []*plansv1.Price{{MeterCode: "nope", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "1"}}}}}},
		"a duplicate currency":          {{Currency: "USD", Prices: []*plansv1.Price{perUnit("1")}}, {Currency: "USD", Prices: []*plansv1.Price{perUnit("1")}}},
	} {
		_, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "metered", BaseFeeMinor: 100,
			Prices: []*plansv1.Price{perUnit("10")}, Fees: []*plansv1.CurrencyFee{{Currency: "USD", BaseFeeMinor: 5}}, CurrencyPrices: cps})
		if code(t, err) != codes.InvalidArgument {
			t.Errorf("currency prices with %s: %v", name, err)
		}
	}
	mv, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "metered", BaseFeeMinor: 100, Prices: []*plansv1.Price{perUnit("10")},
		Fees:           []*plansv1.CurrencyFee{{Currency: "USD", BaseFeeMinor: 5}, {Currency: "EUR", BaseFeeMinor: 4}},
		CurrencyPrices: []*plansv1.CurrencyPrices{{Currency: "USD", Prices: []*plansv1.Price{perUnit("0.25")}}}})
	if err != nil || len(mv.GetPlanVersion().GetCurrencyPrices()) != 1 || mv.GetPlanVersion().GetCurrencyPrices()[0].GetCurrency() != "USD" {
		t.Fatalf("publish with currency prices = %v, %v", mv, err)
	}
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "us", Name: "Sam", Email: "s@example.com", BillingCurrency: "USD"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "fr", Name: "Luc", Email: "l@example.com", BillingCurrency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	if u, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "us", PlanCode: "metered"}); err != nil || u.GetSubscription().GetCurrency() != "USD" {
		t.Fatalf("metered in USD = %v, %v", u, err)
	}
	// EUR has a fee but no usage price: a metered plan cannot be sold in it.
	if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "fr", PlanCode: "metered"}); code(t, err) != codes.FailedPrecondition {
		t.Fatalf("metered in EUR without prices: %v", err)
	}

	// The subscription takes the customer's billing currency, and an explicit currency overrides it.
	r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "de", PlanCode: "team"})
	if err != nil || r.GetSubscription().GetCurrency() != "EUR" {
		t.Fatalf("subscription currency = %v, %v", r, err)
	}
	if _, err := e.plans.CancelSubscription(ctx, &plansv1.CancelSubscriptionRequest{SubscriptionId: r.GetSubscription().GetId()}); err != nil {
		t.Fatal(err)
	}
	r, err = e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "de", PlanCode: "team", Currency: "USD",
		StartTime: timestamppb.New(time.Now().Add(time.Minute))})
	if err != nil || r.GetSubscription().GetCurrency() != "USD" {
		t.Fatalf("explicit currency = %v, %v", r, err)
	}
	// A currency the plan has no fee in cannot be sold.
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "ch", Name: "Heidi", Email: "h@example.com", BillingCurrency: "CHF"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "ch", PlanCode: "team"}); code(t, err) != codes.FailedPrecondition {
		t.Fatalf("no CHF fee: %v", err)
	}
	if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "ch", PlanCode: "team", Currency: "QQQ"}); code(t, err) != codes.InvalidArgument {
		t.Fatalf("unsupported currency: %v", err)
	}
}
