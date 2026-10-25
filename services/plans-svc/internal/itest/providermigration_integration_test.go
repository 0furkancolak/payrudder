//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// A yearly subscription whose fee is invoiced in advance keeps paying in advance after a provider migration: the successor is not "arrears", which would
// leave the year unbilled.
func testProviderMigrationKeepsTheFeeTiming(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Migration fee timing")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 3000, Fees: yearlyFees("basic", 3000)}); err != nil {
		t.Fatal(err)
	}
	year := plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	feeDueOf := func(id string, now time.Time) *plansv1.FeeDue {
		t.Helper()
		l, err := e.plans.ListSubscriptionsFeeDue(ctx, &plansv1.ListSubscriptionsFeeDueRequest{Now: timestamppb.New(now)})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range l.GetDue() {
			if d.GetSubscription().GetId() == id {
				return d
			}
		}
		return nil
	}
	adjustmentsOf := func(id string) (n int) {
		t.Helper()
		l, err := e.plans.ListSubscriptionAdjustments(ctx, &plansv1.ListSubscriptionAdjustmentsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range l.GetAdjustments() {
			if a.GetSubscription().GetId() == id {
				n++
			}
		}
		return n
	}

	t.Run("Payrudder to Payrudder continues the paid year", func(t *testing.T) {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_pm_a", Name: "a", Email: "a@example.com", Country: "TR", BillingCurrency: "TRY"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-48 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_pm_a", PlanCode: "basic", Provider: "iyzico", Interval: year, StartTime: timestamppb.New(start)})
		if err != nil {
			t.Fatal(err)
		}
		old := r.GetSubscription()
		if old.GetFeeTiming() != plansv1.FeeTiming_FEE_TIMING_ADVANCE {
			t.Fatalf("fixture fee timing = %v", old.GetFeeTiming())
		}
		if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: old.GetId(), PeriodStart: old.GetStartTime()}); err != nil {
			t.Fatal(err)
		}
		st, err := e.plans.StartProviderMigration(ctx, &plansv1.StartProviderMigrationRequest{SubscriptionId: old.GetId(), ToProvider: "banktransfer"})
		if err != nil || st.GetMigration().GetNewSubscriptionId() == "" {
			t.Fatalf("migration = %v, %v", st, err)
		}
		nw, err := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: st.GetMigration().GetNewSubscriptionId()})
		if err != nil {
			t.Fatal(err)
		}
		if nw.GetSubscription().GetFeeTiming() != plansv1.FeeTiming_FEE_TIMING_ADVANCE {
			t.Fatalf("successor fee timing = %v; want advance", nw.GetSubscription().GetFeeTiming())
		}
		// The year is paid for through the end of the old period: no credit for it, and the next fee is the one the old subscription would have invoiced.
		if n := adjustmentsOf(old.GetId()); n != 0 {
			t.Errorf("%d adjustment(s) for a year that stays paid", n)
		}
		next := start.AddDate(1, 0, 0)
		d := feeDueOf(nw.GetSubscription().GetId(), next.Add(time.Hour))
		if d == nil || d.GetPeriodStart().AsTime().Sub(next).Abs() > time.Hour {
			t.Fatalf("fee due of the successor = %v; want the period starting %s", d, next)
		}
	})

	t.Run("a provider's yearly subscription comes back to Payrudder billed in advance", func(t *testing.T) {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_pm_b", Name: "b", Email: "b@example.com", Country: "US", BillingCurrency: "USD"}); err != nil {
			t.Fatal(err)
		}
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_pm_b", PlanCode: "basic", Provider: "iyzico", Interval: year,
			StartTime: timestamppb.New(time.Now().Add(-2 * time.Hour))})
		if err != nil {
			t.Fatal(err)
		}
		st, err := e.plans.StartProviderMigration(ctx, &plansv1.StartProviderMigrationRequest{SubscriptionId: r.GetSubscription().GetId(), ToProvider: "paddle"})
		if err != nil {
			t.Fatal(err)
		}
		done, err := e.plans.CompleteProviderMigration(ctx, &plansv1.CompleteProviderMigrationRequest{MigrationId: st.GetMigration().GetId(), ProviderRef: "sub_PM_B",
			CutoverTime: timestamppb.New(time.Now().Add(-time.Minute))})
		if err != nil {
			t.Fatal(err)
		}
		cut := time.Now().Add(10 * 24 * time.Hour).Truncate(time.Microsecond)
		back, err := e.plans.StartProviderMigration(ctx, &plansv1.StartProviderMigrationRequest{SubscriptionId: done.GetSubscription().GetId(), ToProvider: "iyzico", CutoverTime: timestamppb.New(cut)})
		if err != nil || back.GetMigration().GetNewSubscriptionId() == "" {
			t.Fatalf("back = %v, %v", back, err)
		}
		nw, err := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: back.GetMigration().GetNewSubscriptionId()})
		if err != nil {
			t.Fatal(err)
		}
		if nw.GetSubscription().GetFeeTiming() != plansv1.FeeTiming_FEE_TIMING_ADVANCE || nw.GetSubscription().GetInterval() != year {
			t.Fatalf("successor = %v; want a yearly subscription billed in advance", nw.GetSubscription())
		}
		d := feeDueOf(nw.GetSubscription().GetId(), cut.Add(time.Second))
		if d == nil || !d.GetPeriodStart().AsTime().Equal(cut) {
			t.Fatalf("fee due of the successor = %v; want the year starting at the cutover %s", d, cut)
		}
	})
}
