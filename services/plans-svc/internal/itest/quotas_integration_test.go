//go:build integration

package itest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/outbox"
)

// Meters carry a category of the tenant's own, plan versions publish quota windows, modes and included quantities, and a customer's override
// replaces its plan's entitlement in what limits-svc is sent, until it is deleted.
func testQuotasAndOverrides(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Quotas")[plansv1.Environment_ENVIRONMENT_LIVE]
	tenantID := live.GetId()
	ctx := as(tenantID)
	must := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	m, err := e.plans.CreateMeter(ctx, &plansv1.CreateMeterRequest{Code: "tokens", Aggregation: plansv1.Aggregation_AGGREGATION_SUM, ValueProperty: "n",
		UnitLabel: "jeton", Category: "Yapay zekâ"})
	if err != nil || m.GetMeter().GetCategory() != "Yapay zekâ" {
		t.Fatalf("meter = %v, %v", m, err)
	}
	must(e.plans.CreateMeter(ctx, &plansv1.CreateMeterRequest{Code: "seats", Aggregation: plansv1.Aggregation_AGGREGATION_MAX, ValueProperty: "n", UnitLabel: "seats"}))
	list, err := e.plans.ListMeters(ctx, &plansv1.ListMetersRequest{})
	if err != nil || len(list.GetMeters()) != 2 || list.GetMeters()[1].GetCategory() != "Yapay zekâ" || list.GetMeters()[0].GetCategory() != "" {
		t.Fatalf("meters = %v, %v", list, err)
	}

	must(e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "ai", Name: "AI"}))
	tokens := &plansv1.Price{MeterCode: "tokens", Included: "1000", Model: &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: "0.02"}}}
	pv, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "ai", BaseFeeMinor: 1000, Prices: []*plansv1.Price{tokens},
		Entitlements: []*plansv1.Entitlement{
			{MeterCode: "tokens", Limit: "5000", AlertThresholdsPct: []int32{50, 80, 100}, Window: plansv1.QuotaWindow_QUOTA_WINDOW_DAY, Mode: plansv1.QuotaMode_QUOTA_MODE_SOFT},
		}})
	if err != nil {
		t.Fatal(err)
	}
	got := pv.GetPlanVersion()
	if got.GetPrices()[0].GetIncluded() != "1000" || got.GetEntitlements()[0].GetWindow() != plansv1.QuotaWindow_QUOTA_WINDOW_DAY ||
		got.GetEntitlements()[0].GetMode() != plansv1.QuotaMode_QUOTA_MODE_SOFT || len(got.GetEntitlements()[0].GetAlertThresholdsPct()) != 3 {
		t.Fatalf("plan version = %v", got)
	}
	// Soft without a price on the meter is refused.
	_, err = e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "ai", BaseFeeMinor: 1000,
		Entitlements: []*plansv1.Entitlement{{MeterCode: "seats", Limit: "3", Mode: plansv1.QuotaMode_QUOTA_MODE_SOFT}}})
	if code(t, err) != codes.InvalidArgument {
		t.Fatalf("soft without a price: %v", err)
	}

	must(e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_q", Name: "Q", Email: "q@example.com"}))
	must(e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_q", PlanCode: "ai"}))

	relay := func() map[string]*eventsv1.EntitlementState {
		t.Helper()
		sink := &captureSink{}
		r, err := outbox.NewRelay(e.system, sink, outbox.Config{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for {
			n, err := r.RelayOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
		}
		// The last row of each key is what the compacted topic keeps.
		out := map[string]*eventsv1.EntitlementState{}
		for _, row := range sink.rows {
			if row.TenantID != tenantID || row.Topic != "plans.entitlements.v1" {
				continue
			}
			if row.Payload == nil {
				out[row.PartitionKey] = nil
				continue
			}
			var st eventsv1.EntitlementState
			if err := proto.Unmarshal(row.Payload, &st); err != nil {
				t.Fatal(err)
			}
			out[row.PartitionKey] = &st
		}
		return out
	}
	byMeter := func(states map[string]*eventsv1.EntitlementState, meter string) (*eventsv1.EntitlementState, bool) {
		for k, st := range states {
			if len(k) > len(meter) && k[len(k)-len(meter)-1:] == "/"+meter {
				return st, true
			}
		}
		return nil, false
	}
	states := relay()
	if st, ok := byMeter(states, "tokens"); !ok || st.GetWindow() != "day" || st.GetMode() != "soft" || st.GetLimitUnits() != 5000 || st.GetOverride() {
		t.Fatalf("plan entitlement = %v", states)
	}

	// An override replaces the plan's entitlement, and another adds a limit on a meter the plan does not limit.
	o, err := e.plans.SetEntitlementOverride(ctx, &plansv1.SetEntitlementOverrideRequest{CustomerExternalId: "cus_q", MeterCode: "tokens", Limit: "20000",
		Window: plansv1.QuotaWindow_QUOTA_WINDOW_WEEK, Mode: plansv1.QuotaMode_QUOTA_MODE_HARD, AlertThresholdsPct: []int32{90}})
	if err != nil || o.GetOverride().GetLimit() != "20000" || o.GetOverride().GetWindow() != plansv1.QuotaWindow_QUOTA_WINDOW_WEEK {
		t.Fatalf("override = %v, %v", o, err)
	}
	must(e.plans.SetEntitlementOverride(ctx, &plansv1.SetEntitlementOverrideRequest{CustomerExternalId: "cus_q", MeterCode: "seats", Limit: "5", Mode: plansv1.QuotaMode_QUOTA_MODE_NOTIFY}))
	states = relay()
	if st, _ := byMeter(states, "tokens"); st == nil || !st.GetOverride() || st.GetLimitUnits() != 20000 || st.GetWindow() != "week" || st.GetMode() != "hard" || len(st.GetAlertThresholdsPct()) != 1 {
		t.Fatalf("overridden tokens = %v", st)
	}
	if st, _ := byMeter(states, "seats"); st == nil || st.GetLimitUnits() != 5 || st.GetMode() != "notify" || st.GetWindow() != "month" {
		t.Fatalf("override-only seats = %v", st)
	}
	ol, err := e.plans.ListEntitlementOverrides(ctx, &plansv1.ListEntitlementOverridesRequest{CustomerExternalId: "cus_q"})
	if err != nil || len(ol.GetOverrides()) != 2 || ol.GetOverrides()[0].GetMeterCode() != "seats" {
		t.Fatalf("overrides = %v, %v", ol, err)
	}
	// Soft needs the customer's package to price the meter: seats is not priced.
	_, err = e.plans.SetEntitlementOverride(ctx, &plansv1.SetEntitlementOverrideRequest{CustomerExternalId: "cus_q", MeterCode: "seats", Limit: "5", Mode: plansv1.QuotaMode_QUOTA_MODE_SOFT})
	if code(t, err) != codes.InvalidArgument {
		t.Fatalf("soft override on an unpriced meter: %v", err)
	}
	if _, err := e.plans.SetEntitlementOverride(ctx, &plansv1.SetEntitlementOverrideRequest{CustomerExternalId: "cus_q", MeterCode: "nope", Limit: "1"}); code(t, err) != codes.InvalidArgument {
		t.Fatalf("override on an unknown meter: %v", err)
	}
	if _, err := e.plans.SetEntitlementOverride(ctx, &plansv1.SetEntitlementOverrideRequest{CustomerExternalId: "nobody", MeterCode: "seats", Limit: "1"}); code(t, err) != codes.NotFound {
		t.Fatalf("override of an unknown customer: %v", err)
	}

	// Deleting returns the plan's entitlement on a meter the plan limits, and ends the one only the override gave.
	must(e.plans.DeleteEntitlementOverride(ctx, &plansv1.DeleteEntitlementOverrideRequest{CustomerExternalId: "cus_q", MeterCode: "tokens"}))
	must(e.plans.DeleteEntitlementOverride(ctx, &plansv1.DeleteEntitlementOverrideRequest{CustomerExternalId: "cus_q", MeterCode: "seats"}))
	states = relay()
	if st, _ := byMeter(states, "tokens"); st == nil || st.GetOverride() || st.GetLimitUnits() != 5000 || st.GetWindow() != "day" {
		t.Fatalf("tokens after delete = %v", st)
	}
	if st, ok := byMeter(states, "seats"); !ok || st != nil {
		t.Fatalf("seats after delete must be a tombstone: %v (%v)", st, ok)
	}
	if _, err := e.plans.DeleteEntitlementOverride(ctx, &plansv1.DeleteEntitlementOverrideRequest{CustomerExternalId: "cus_q", MeterCode: "seats"}); code(t, err) != codes.NotFound {
		t.Fatalf("second delete: %v", err)
	}
	// Another tenant sees none of it.
	other := e.account(t, "Quotas other")[plansv1.Environment_ENVIRONMENT_LIVE]
	if _, err := e.plans.ListEntitlementOverrides(as(other.GetId()), &plansv1.ListEntitlementOverridesRequest{CustomerExternalId: "cus_q"}); code(t, err) != codes.NotFound {
		t.Fatalf("other tenant: %v", err)
	}
}
