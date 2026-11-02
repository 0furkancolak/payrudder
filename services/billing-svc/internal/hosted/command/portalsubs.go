package command

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
)

// SubscriptionReader is the part of plans-svc's PlansService the portal's subscription summary reads. It is optional on the backend (BackendConfig.Subscriptions).
type SubscriptionReader interface {
	ListSubscriptions(ctx context.Context, in *plansv1.ListSubscriptionsRequest, opts ...grpc.CallOption) (*plansv1.ListSubscriptionsResponse, error)
	ListPlans(ctx context.Context, in *plansv1.ListPlansRequest, opts ...grpc.CallOption) (*plansv1.ListPlansResponse, error)
	GetPlanVersion(ctx context.Context, in *plansv1.GetPlanVersionRequest, opts ...grpc.CallOption) (*plansv1.GetPlanVersionResponse, error)
}

// portalSubscriptionLimit is how many subscriptions of one customer the summary reads. A customer has one active subscription at a time (plans-svc refuses
// overlapping ones), so a page is far more than the portal needs.
const portalSubscriptionLimit = 20

// PortalSubscription is a subscription of the portal's customer as the summary shows it. Nothing in it is a decision billing took: it is what plans-svc says.
type PortalSubscription struct {
	ID, PlanCode, PlanName, State, Interval string
	Quantity                                int32
	// PriceKnown is false when the plan version has no fee for the interval and currency; PriceMinor is the fixed fee of one period times the quantity.
	PriceKnown        bool
	PriceMinor        int64
	Currency          string
	TrialEnd          *time.Time
	NextRenewal       *time.Time
	CancelAtPeriodEnd bool
	End               *time.Time
	ScheduledPlanName string
	Scheduled         *time.Time
	ProviderManaged   bool
}

// PortalSubscriptions lists the customer's current subscriptions for the portal: those that are trialing, active, past due or paused. A customer the tenant does not know
// by an external id has none to list. The dates are the ones plans-svc gives exactly (a trial's end, the day a waiting plan change takes effect, the end of a subscription
// that cancels with its period); a plain active subscription has no next date because plans-svc does not expose when its next fee is due.
func (b *Backend) PortalSubscriptions(ctx context.Context, tenantID uuid.UUID, externalID string) ([]PortalSubscription, error) {
	if b.cfg.Subscriptions == nil || externalID == "" {
		return nil, nil
	}
	tctx := inputs.TenantCtx(ctx, tenantID)
	list, err := b.cfg.Subscriptions.ListSubscriptions(tctx, &plansv1.ListSubscriptionsRequest{CustomerExternalId: externalID, PageSize: portalSubscriptionLimit})
	if err != nil {
		return nil, err
	}
	var live []*plansv1.Subscription
	for _, s := range list.GetSubscriptions() {
		switch s.GetState() {
		case plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING, plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE,
			plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE, plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED:
			live = append(live, s)
		}
	}
	if len(live) == 0 {
		return nil, nil
	}
	tenantCurrency, err := b.TenantCurrency(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	if plans, err := b.cfg.Subscriptions.ListPlans(tctx, &plansv1.ListPlansRequest{}); err == nil {
		for _, p := range plans.GetPlans() {
			names[p.GetCode()] = p.GetName()
		}
	} // a plan list that cannot be read leaves the codes as names: the summary is still worth showing
	name := func(code string) string {
		if n := names[code]; n != "" {
			return n
		}
		return code
	}
	out := make([]PortalSubscription, 0, len(live))
	for _, s := range live {
		ps := PortalSubscription{ID: s.GetId(), PlanCode: s.GetPlanCode(), PlanName: name(s.GetPlanCode()), State: stateWord(s.GetState()), Interval: intervalWord(s.GetInterval()),
			Quantity: max(s.GetQuantity(), 1), Currency: s.GetCurrency(), CancelAtPeriodEnd: s.GetCancelAtPeriodEnd(), ProviderManaged: s.GetOwner() == "provider"}
		if resp, err := b.cfg.Subscriptions.GetPlanVersion(tctx, &plansv1.GetPlanVersionRequest{PlanVersionId: s.GetPlanVersionId()}); err == nil && resp.GetPlanVersion() != nil {
			ps.PriceMinor, ps.PriceKnown = periodFee(resp.GetPlanVersion(), tenantCurrency, s.GetCurrency(), s.GetInterval(), ps.Quantity)
		}
		if t := s.GetTrialEndTime(); t != nil && s.GetState() == plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING {
			v := t.AsTime()
			ps.TrialEnd, ps.NextRenewal = &v, &v
		}
		if t := s.GetEndTime(); t != nil && s.GetCancelAtPeriodEnd() {
			v := t.AsTime()
			ps.End = &v
		}
		if c := s.GetScheduledChange(); c != nil && c.GetEffectiveTime() != nil && !s.GetCancelAtPeriodEnd() {
			v := c.GetEffectiveTime().AsTime()
			ps.Scheduled, ps.NextRenewal, ps.ScheduledPlanName = &v, &v, name(c.GetPlanCode())
		}
		out = append(out, ps)
	}
	return out, nil
}

func stateWord(s plansv1.SubscriptionState) string {
	switch s {
	case plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING:
		return "trialing"
	case plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE:
		return "past_due"
	case plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED:
		return "paused"
	}
	return "active"
}

func intervalWord(i plansv1.SubscriptionInterval) string {
	if i == plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR {
		return "year"
	}
	return "month"
}

// periodFee is the plan version's fixed fee for one period of the interval in the currency, times the quantity, as plans-svc prices it (plans/query.FeeIn): the base fee
// for a month in the tenant's currency, else the fee row of the currency and interval. A fee row without an interval is a month. ok is false when there is none.
func periodFee(pv *plansv1.PlanVersion, tenantCurrency, currency string, interval plansv1.SubscriptionInterval, quantity int32) (fee int64, ok bool) {
	monthly := interval != plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	switch {
	case monthly && currency == tenantCurrency:
		fee, ok = pv.GetBaseFeeMinor(), true
	default:
		for _, f := range pv.GetFees() {
			fy := f.GetInterval() == plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
			if f.GetCurrency() == currency && fy == !monthly {
				fee, ok = f.GetBaseFeeMinor(), true
			}
		}
	}
	if !ok || fee > (1<<62)/int64(max(quantity, 1)) {
		return 0, false
	}
	return fee * int64(max(quantity, 1)), true
}
