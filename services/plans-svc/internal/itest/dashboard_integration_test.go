//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
)

// testDashboardFacts: the subscription report can be split by plan (one row per plan and currency, MRR never added across currencies), and the customer stats
// count the customers created in a range and the range before it, for the caller's tenant only.
func testDashboardFacts(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Dashboard facts")[plansv1.Environment_ENVIRONMENT_LIVE] // tenant currency TRY
	ctx := as(live.GetId())
	for _, p := range []struct {
		code string
		fee  int64
	}{{"pro", 9900}, {"basic", 2900}} {
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: p.code, Name: p.code}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: p.code, BaseFeeMinor: p.fee, Fees: []*plansv1.CurrencyFee{{Currency: "EUR", BaseFeeMinor: p.fee / 4}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ id, plan, currency string }{{"a", "pro", "TRY"}, {"b", "pro", "EUR"}, {"c", "pro", "EUR"}, {"d", "basic", "TRY"}} {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: c.id, Name: c.id, Email: c.id + "@example.com", BillingCurrency: c.currency}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: c.id, PlanCode: c.plan}); err != nil {
			t.Fatal(err)
		}
	}

	r, err := e.plans.GetSubscriptionReport(ctx, &plansv1.GetSubscriptionReportRequest{GroupBy: []string{"plan"}})
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ plan, currency string }
	got := map[key]*plansv1.SubscriptionReportRow{}
	for _, row := range r.GetRows() {
		got[key{row.GetPlanCode(), row.GetCurrency()}] = row
	}
	if len(got) != 3 {
		t.Fatalf("rows by plan = %v", r.GetRows())
	}
	if x := got[key{"pro", "TRY"}]; x == nil || x.GetActive() != 1 || x.GetMrrMinor() != 9900 {
		t.Errorf("pro TRY = %v", x)
	}
	if x := got[key{"pro", "EUR"}]; x == nil || x.GetActive() != 2 || x.GetMrrMinor() != 2*2475 {
		t.Errorf("pro EUR = %v (two subscriptions, 24.75 each; never added to TRY)", x)
	}
	if x := got[key{"basic", "TRY"}]; x == nil || x.GetActive() != 1 || x.GetMrrMinor() != 2900 {
		t.Errorf("basic TRY = %v", x)
	}
	// Without the split nothing carries a plan, as before.
	if r, err := e.plans.GetSubscriptionReport(ctx, &plansv1.GetSubscriptionReportRequest{}); err != nil || len(r.GetRows()) != 2 || r.GetRows()[0].GetPlanCode() != "" {
		t.Errorf("unsplit report = %v, %v", r.GetRows(), err)
	}

	now := time.Now()
	stats, err := e.plans.GetCustomerStats(ctx, &plansv1.GetCustomerStatsRequest{
		FromTime: timestamppb.New(now.Add(-time.Hour)), ToTime: timestamppb.New(now.Add(time.Hour)),
		PrevFromTime: timestamppb.New(now.Add(-48 * time.Hour)), PrevToTime: timestamppb.New(now.Add(-time.Hour))})
	if err != nil || stats.GetTotal() != 4 || stats.GetNewInRange() != 4 || stats.GetNewInPrevious() != 0 {
		t.Errorf("stats = %v, %v", stats, err)
	}
	if stats, err = e.plans.GetCustomerStats(ctx, &plansv1.GetCustomerStatsRequest{}); err != nil || stats.GetTotal() != 4 || stats.GetNewInRange() != 0 || stats.GetNewInPrevious() != 0 {
		t.Errorf("stats without ranges = %v, %v", stats, err)
	}
	if _, err = e.plans.GetCustomerStats(ctx, &plansv1.GetCustomerStatsRequest{FromTime: timestamppb.New(now)}); code(t, err) != codes.InvalidArgument {
		t.Errorf("half a range: %v", err)
	}
	if _, err = e.plans.GetCustomerStats(ctx, &plansv1.GetCustomerStatsRequest{FromTime: timestamppb.New(now), ToTime: timestamppb.New(now.Add(-time.Hour))}); code(t, err) != codes.InvalidArgument {
		t.Errorf("a range that ends before it starts: %v", err)
	}
	// Another tenant has no customers of these.
	other := e.account(t, "Somebody else")[plansv1.Environment_ENVIRONMENT_LIVE]
	if stats, err = e.plans.GetCustomerStats(as(other.GetId()), &plansv1.GetCustomerStatsRequest{}); err != nil || stats.GetTotal() != 0 {
		t.Errorf("other tenant's stats = %v, %v", stats, err)
	}
}
