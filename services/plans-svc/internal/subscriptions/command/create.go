package command

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/payments/providers"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	couponscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	entitlementscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/entitlements/command"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	routingqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/routing/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
	webhookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// createArgs is what a CreateSubscription request says before the database is read.
type createArgs struct {
	start    time.Time
	interval subscription.Interval
	provider *string
}

func (c *Commands) parseCreate(req *plansv1.CreateSubscriptionRequest) (createArgs, error) {
	a := createArgs{start: c.now()}
	if req.GetStartTime() != nil {
		if err := req.GetStartTime().CheckValid(); err != nil {
			return a, domain.Invalid("start_time", "invalid timestamp")
		}
		a.start = req.GetStartTime().AsTime()
	}
	if d := req.GetTrialDays(); d < 0 || d > 365 {
		return a, domain.Invalid("trial_days", "must be between 0 and 365")
	}
	var err error
	if a.interval, err = intervalFromProto(req.GetInterval()); err != nil {
		return a, err
	}
	if p := req.GetProvider(); p != "" {
		if !domain.ValidProviderName(p) {
			return a, domain.Invalid("provider", "a lower-case provider name such as paddle")
		}
		a.provider = &p
	}
	return a, nil
}

func intervalFromProto(i plansv1.SubscriptionInterval) (subscription.Interval, error) {
	switch i {
	case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_UNSPECIFIED, plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH:
		return subscription.Monthly, nil
	case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR:
		return subscription.Yearly, nil
	}
	return "", domain.Invalid("interval", "unknown interval")
}

// Create starts a subscription of a customer on a plan version, with its coupon, webhook event and entitlements.
func (c *Commands) Create(ctx context.Context, req *plansv1.CreateSubscriptionRequest) (*plansv1.Subscription, error) {
	a, err := c.parseCreate(req)
	if err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = c.create(ctx, q, ti.ID, req, a)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

func (c *Commands) create(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, req *plansv1.CreateSubscriptionRequest, a createArgs) (*plansv1.Subscription, error) {
	t, err := tenantsqry.Current(ctx, q)
	if err != nil {
		return nil, err
	}
	loc, err := tenantsqry.Location(t.Timezone)
	if err != nil {
		return nil, err
	}
	cust, err := q.GetCustomerByExternalID(ctx, req.GetCustomerExternalId())
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	pv, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: req.GetPlanCode(), Version: req.GetPlanVersion()})
	if err != nil {
		return nil, convert.Err(err, "plan version")
	}
	currency, err := subscriptionCurrency(req.GetCurrency(), cust.BillingCurrency, t.Currency)
	if err != nil {
		return nil, err
	}
	if _, err := plansqry.FeeIn(ctx, q, pv.ID, pv.BaseFeeMinor, t.Currency, currency, a.interval); err != nil {
		return nil, err
	}
	if err := plansqry.RequireUsagePrices(ctx, q, pv.ID, t.Currency, currency); err != nil {
		return nil, err
	}
	provider, providerRenews, err := chooseProvider(ctx, q, a.provider, convert.Str(cust.Country), currency)
	if err != nil {
		return nil, err
	}
	state, trialEnds := initialState(a.start, loc, int(req.GetTrialDays()))
	timing, err := feeTimingFor(req.GetFeeTiming(), a.interval, providerRenews)
	if err != nil {
		return nil, err
	}
	anchor, feeDue := billingAnchor(timing, a.start, trialEnds, loc)
	r, err := q.InsertSubscription(ctx, sqlc.InsertSubscriptionParams{
		ID: idgen.NewUUIDv7(), CustomerID: cust.ID, PlanVersionID: pv.ID, StartTime: a.start,
		State: string(state), Interval: string(a.interval), Currency: currency, TrialEndsAt: trialEnds,
		BillingAnchor: anchor, Provider: provider, Owner: domain.OwnerPayrudder, FeeTiming: timing, FeeDueAt: feeDue,
	})
	if err != nil {
		return nil, err
	}
	return c.announceCreated(ctx, q, tenantID, r, req.GetCouponCode())
}

// subscriptionCurrency is the currency the request names, else the customer's billing currency, else the tenant's.
func subscriptionCurrency(requested string, customerBilling *string, tenantCurrency string) (string, error) {
	currency := requested
	if currency == "" && customerBilling != nil {
		currency = *customerBilling
	}
	if currency == "" {
		currency = tenantCurrency
	}
	if _, ok := money.LookupCurrency(currency); !ok {
		return "", domain.Invalid("currency", "unsupported currency")
	}
	return currency, nil
}

// chooseProvider returns the provider the request names or, when it names none, the one the routing rules choose from the
// customer's country and the subscription's currency, and whether that provider renews the subscription itself.
func chooseProvider(ctx context.Context, q *sqlc.Queries, named *string, country, currency string) (provider *string, renews bool, err error) {
	provider = named
	if provider == nil {
		rules, err := routingqry.Rules(ctx, q)
		if err != nil {
			return nil, false, err
		}
		if routed, _, ok := domain.Route(rules, country, currency); ok {
			provider = &routed
		}
	}
	if provider == nil {
		return nil, false, nil
	}
	caps, known := providers.Lookup(*provider)
	switch {
	case !known:
		return nil, false, domain.Invalid("provider", "unknown provider "+*provider+"; known: "+strings.Join(providers.Names(), ", "))
	case !caps.CanCharge(currency):
		return nil, false, errkey.Newf(codes.FailedPrecondition, "plans.subscription.provider_currency", "%s cannot charge in %s", *provider, currency)
	}
	return provider, caps.OwnsRenewal, nil
}

// initialState is Trialing with the end of the trial when the subscription has trial days, else Active.
func initialState(start time.Time, loc *time.Location, trialDays int) (subscription.State, *time.Time) {
	if trialDays <= 0 {
		return subscription.Active, nil
	}
	e := start.In(loc).AddDate(0, 0, trialDays)
	return subscription.Trialing, &e
}

// feeTimingFor decides when the fixed fee is invoiced. A subscription a provider renews keeps the old arrears behaviour
// (the provider bills its fee). Otherwise the fee is invoiced in advance unless arrears is asked for; a yearly subscription
// cannot wait a year to be charged.
func feeTimingFor(asked plansv1.FeeTiming, interval subscription.Interval, providerRenews bool) (string, error) {
	arrears := asked == plansv1.FeeTiming_FEE_TIMING_ARREARS
	switch {
	case providerRenews:
		return subscription.FeeInArrears, nil
	case arrears && interval == subscription.Yearly:
		return "", domain.Invalid("fee_timing", "a yearly subscription is invoiced in advance")
	case arrears:
		return subscription.FeeInArrears, nil
	}
	return subscription.FeeInAdvance, nil
}

// billingAnchor: billing periods of an advance subscription start where its fee does, on the day it starts or when its
// trial ends. An arrears subscription is billed by calendar month.
func billingAnchor(timing string, start time.Time, trialEnds *time.Time, loc *time.Location) (anchor time.Time, feeDue *time.Time) {
	if timing != subscription.FeeInAdvance {
		return monthStart(start, loc), nil
	}
	anchor = start
	if trialEnds != nil {
		anchor = *trialEnds
	}
	return anchor, &anchor
}

// monthStart is the first instant of the calendar month t falls in, in loc: the anchor of a subscription's billing periods,
// which therefore are the calendar months billing-svc closes.
func monthStart(t time.Time, loc *time.Location) time.Time {
	y, m, _ := t.In(loc).Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, loc)
}

// announceCreated redeems the coupon of a new subscription, tells the tenant's webhooks and publishes its entitlements.
func (c *Commands) announceCreated(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, r sqlc.InsertSubscriptionRow, couponCode string) (*plansv1.Subscription, error) {
	full, err := q.GetSubscription(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	if couponCode != "" {
		if err := couponscmd.Redeem(ctx, q, c.now(), full, couponCode); err != nil {
			return nil, err
		}
	}
	out := convert.SubscriptionOf(full, c.now())
	if err := subsqry.AttachExtras(ctx, q, out); err != nil {
		return nil, err
	}
	if err := webhookscmd.EmitSubscription(ctx, q, c.now(), webhooksmodel.SubscriptionCreated, out, nil); err != nil {
		return nil, err
	}
	return out, entitlementscmd.Publish(ctx, q, tenantID, r.CustomerID, r.PlanVersionID, r.StartTime, nil, c.now())
}
