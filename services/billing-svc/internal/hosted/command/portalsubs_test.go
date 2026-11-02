package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
)

type fakeSubs struct {
	subs     []*plansv1.Subscription
	plans    []*plansv1.Plan
	versions map[string]*plansv1.PlanVersion
	plansErr error
	asked    []string
}

func (f *fakeSubs) ListSubscriptions(_ context.Context, in *plansv1.ListSubscriptionsRequest, _ ...grpc.CallOption) (*plansv1.ListSubscriptionsResponse, error) {
	f.asked = append(f.asked, in.GetCustomerExternalId())
	return &plansv1.ListSubscriptionsResponse{Subscriptions: f.subs}, nil
}

func (f *fakeSubs) ListPlans(context.Context, *plansv1.ListPlansRequest, ...grpc.CallOption) (*plansv1.ListPlansResponse, error) {
	if f.plansErr != nil {
		return nil, f.plansErr
	}
	return &plansv1.ListPlansResponse{Plans: f.plans}, nil
}

func (f *fakeSubs) GetPlanVersion(_ context.Context, in *plansv1.GetPlanVersionRequest, _ ...grpc.CallOption) (*plansv1.GetPlanVersionResponse, error) {
	pv, ok := f.versions[in.GetPlanVersionId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "no such plan version")
	}
	return &plansv1.GetPlanVersionResponse{PlanVersion: pv}, nil
}

type fakeTenants struct{ inputs.Admin }

func (fakeTenants) GetTenant(context.Context, *plansv1.GetTenantRequest, ...grpc.CallOption) (*plansv1.GetTenantResponse, error) {
	return &plansv1.GetTenantResponse{Tenant: &plansv1.Tenant{Currency: "USD"}}, nil
}

func ts(y int, m time.Month, d int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
}

func backendWith(f *fakeSubs) *Backend {
	return &Backend{cfg: BackendConfig{Subscriptions: f}, in: &inputs.Inputs{Admin: fakeTenants{}}}
}

func TestPortalSubscriptionsSummary(t *testing.T) {
	f := &fakeSubs{
		plans: []*plansv1.Plan{{Code: "pro", Name: "Pro"}, {Code: "team", Name: "Team"}},
		versions: map[string]*plansv1.PlanVersion{
			"pv-pro": {Id: "pv-pro", PlanCode: "pro", BaseFeeMinor: 2900, Fees: []*plansv1.CurrencyFee{
				{Currency: "USD", BaseFeeMinor: 29000, Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR},
				{Currency: "EUR", BaseFeeMinor: 2700},
			}},
			"pv-team": {Id: "pv-team", PlanCode: "team", BaseFeeMinor: 500},
		},
		subs: []*plansv1.Subscription{
			{Id: "trial", PlanCode: "pro", PlanVersionId: "pv-pro", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING, Currency: "USD", TrialEndTime: ts(2026, 12, 1),
				Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH},
			{Id: "yearly-eur-seats", PlanCode: "pro", PlanVersionId: "pv-pro", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE, Currency: "EUR", Quantity: 3,
				Interval:        plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH,
				ScheduledChange: &plansv1.ScheduledChange{PlanCode: "team", EffectiveTime: ts(2027, 1, 1)}},
			{Id: "ending", PlanCode: "team", PlanVersionId: "pv-team", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE, Currency: "USD", CancelAtPeriodEnd: true, EndTime: ts(2026, 11, 30),
				Owner: "provider"},
			{Id: "yearly", PlanCode: "pro", PlanVersionId: "pv-pro", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE, Currency: "USD",
				Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR},
			{Id: "no-fee", PlanCode: "gone", PlanVersionId: "pv-missing", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED, Currency: "USD"},
			{Id: "over", PlanCode: "pro", PlanVersionId: "pv-pro", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED, Currency: "USD"},
		},
	}
	got, err := backendWith(f).PortalSubscriptions(context.Background(), uuid.New(), "cus_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.asked) != 1 || f.asked[0] != "cus_1" {
		t.Errorf("asked plans-svc for %v, want only cus_1", f.asked)
	}
	byID := map[string]PortalSubscription{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if len(got) != 5 || byID["over"].ID != "" {
		t.Fatalf("a cancelled subscription is not listed: %+v", got)
	}
	tr := byID["trial"]
	if tr.PlanName != "Pro" || tr.State != "trialing" || tr.Interval != "month" || !tr.PriceKnown || tr.PriceMinor != 2900 || tr.NextRenewal == nil || !tr.NextRenewal.Equal(tr.TrialEnd.UTC()) || tr.End != nil {
		t.Errorf("trial = %+v", tr)
	}
	sc := byID["yearly-eur-seats"]
	// EUR is not the tenant's currency: the fee row of EUR (month), times 3 seats; the waiting change to Team is the next renewal.
	if sc.PriceMinor != 8100 || sc.Quantity != 3 || sc.ScheduledPlanName != "Team" || sc.NextRenewal == nil || sc.NextRenewal.Month() != time.January || sc.TrialEnd != nil {
		t.Errorf("seats and a waiting change = %+v", sc)
	}
	en := byID["ending"]
	if !en.CancelAtPeriodEnd || en.End == nil || en.End.Day() != 30 || !en.ProviderManaged || en.NextRenewal != nil || en.PlanName != "Team" {
		t.Errorf("ending = %+v", en)
	}
	// A year in the tenant's currency is the year's fee row, not twelve months of the base fee.
	if y := byID["yearly"]; y.Interval != "year" || y.PriceMinor != 29000 || y.State != "past_due" || y.NextRenewal != nil {
		t.Errorf("yearly = %+v", y)
	}
	// A plan version plans-svc cannot give has no price, and a plan it did not list is shown by its code.
	if n := byID["no-fee"]; n.PriceKnown || n.PlanName != "gone" || n.State != "paused" {
		t.Errorf("no fee = %+v", n)
	}
}

func TestPortalSubscriptionsNeedACustomerAndSurviveNoPlanNames(t *testing.T) {
	f := &fakeSubs{plansErr: errors.New("down"), versions: map[string]*plansv1.PlanVersion{"pv": {BaseFeeMinor: 100}},
		subs: []*plansv1.Subscription{{Id: "a", PlanCode: "pro", PlanVersionId: "pv", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE, Currency: "USD"}}}
	b := backendWith(f)
	if got, err := b.PortalSubscriptions(context.Background(), uuid.New(), ""); err != nil || got != nil || len(f.asked) != 0 {
		t.Errorf("a customer with no external id: %v, %v, asked %v", got, err, f.asked)
	}
	got, err := b.PortalSubscriptions(context.Background(), uuid.New(), "cus_1")
	if err != nil || len(got) != 1 || got[0].PlanName != "pro" {
		t.Errorf("without plan names the code is shown: %+v, %v", got, err)
	}
	if got, err := (&Backend{}).PortalSubscriptions(context.Background(), uuid.New(), "cus_1"); got != nil || err != nil {
		t.Errorf("a backend with no plans client lists nothing: %v, %v", got, err)
	}
}
