// Package query reads the subscriptions a provider runs, and works out what a change of one would do. It never changes Payrudder's data.
package query

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	catalogue "github.com/0furkancolak/payrudder/services/payments-worker/internal/catalogue/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Plans is what the queries ask of plans-svc, as the tenant.
type Plans interface {
	GetSubscription(ctx context.Context, tenant uuid.UUID, id string) (*plansv1.Subscription, error)
	GetPlanVersion(ctx context.Context, tenant uuid.UUID, req *plansv1.GetPlanVersionRequest) (*plansv1.PlanVersion, error)
}

// Deps is what the queries read. Plans nil means plans-svc is not configured.
type Deps struct {
	Plans         Plans
	Objects       store.Objects
	Subscriptions store.Subscriptions
	Log           *slog.Logger
}

// List is the tenant's subscriptions at providers, most recently changed first.
func List(ctx context.Context, d Deps, tenant uuid.UUID) ([]store.ProviderSubscription, error) {
	list, err := d.Subscriptions.List(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("list provider subscriptions: %w", err)
	}
	return list, nil
}

// Load reads a subscription Payrudder mirrors and checks the provider of the target runs it.
func Load(ctx context.Context, d Deps, tg target.Target, id string) (*plansv1.Subscription, error) {
	if d.Plans == nil {
		return nil, refuse.New(http.StatusServiceUnavailable, "payments.plans.not_configured", "plans-svc is not configured")
	}
	sub, err := d.Plans.GetSubscription(ctx, tg.Tenant, id)
	if err != nil {
		return nil, &refuse.Plans{Err: err}
	}
	if sub.GetOwner() != "provider" || sub.GetProvider() != tg.Provider || sub.GetProviderRef() == "" {
		return nil, refuse.New(http.StatusConflict, "payments.subscription.wrong_provider", "this subscription is not run by "+tg.Provider)
	}
	return sub, nil
}

// ChangeRequest is the body of a change: the same words as the tenant API's change request.
type ChangeRequest struct {
	PlanCode    string `json:"plan_code"`
	PlanVersion int32  `json:"plan_version"`
	Interval    string `json:"interval"`
	Timing      string `json:"timing"`
	Proration   string `json:"proration"`
}

// Change is what a request asks the provider to do to a subscription.
type Change struct {
	Provider payments.ProviderChange
	// PlanVersionID and Interval are what Payrudder's copy follows to.
	PlanVersionID string
	Interval      plansv1.SubscriptionInterval
}

// enumWord is a proto enum name as the lower-case word the provider adapters use: CHANGE_TIMING_PERIOD_END is period_end.
func enumWord(v, prefix string) string { return strings.TrimPrefix(strings.ToLower(v), prefix) }

func changeRefused(status int, msg string) error {
	return refuse.New(status, "payments.subscription.change_refused", msg)
}

// PlanChange works out the provider's change for a request: the price of the target plan version and interval, which must be in the catalogue.
func PlanChange(ctx context.Context, d Deps, tg target.Target, sub *plansv1.Subscription, in ChangeRequest) (Change, error) {
	pvID := sub.GetPlanVersionId()
	if in.PlanCode != "" {
		pv, err := d.Plans.GetPlanVersion(ctx, tg.Tenant, &plansv1.GetPlanVersionRequest{PlanCode: in.PlanCode, Version: in.PlanVersion})
		if err != nil {
			return Change{}, changeRefused(http.StatusNotFound, "no such plan version")
		}
		pvID = pv.GetId()
	}
	iv := sub.GetInterval()
	switch enumWord(in.Interval, "subscription_interval_") {
	case "year":
		iv = plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	case "month":
		iv = plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
	case "":
	default:
		return Change{}, changeRefused(http.StatusBadRequest, "interval must be month or year")
	}
	if pvID == sub.GetPlanVersionId() && iv == sub.GetInterval() {
		return Change{}, changeRefused(http.StatusBadRequest, "the subscription is already on this plan version and interval")
	}
	ref, found, err := d.Objects.Get(ctx, tg.Tenant, tg.Provider, tg.Env, catalogue.KindPrice, catalogue.PriceKey(pvID, catalogue.IntervalName(iv), sub.GetCurrency()))
	if err != nil {
		d.Log.ErrorContext(ctx, "provider price not looked up", "provider", tg.Provider, "err", err)
		return Change{}, changeRefused(http.StatusInternalServerError, "internal error")
	}
	if !found {
		return Change{}, changeRefused(http.StatusConflict, fmt.Sprintf("%s has no %sly price in %s for this plan version; sync the catalogue first", tg.Provider, catalogue.IntervalName(iv), sub.GetCurrency()))
	}
	timing := enumWord(in.Timing, "change_timing_")
	if timing == "" {
		timing = "immediately"
	}
	return Change{
		Provider:      payments.ProviderChange{Items: []payments.ChangeItem{{PriceRef: ref, Quantity: 1}}, Timing: timing, Proration: enumWord(in.Proration, "change_proration_")},
		PlanVersionID: pvID, Interval: iv,
	}, nil
}

// Preview is what a change would cost, in the words of the tenant API's change preview. Amounts are minor units as decimal strings.
type Preview struct {
	Timing           string `json:"timing"`
	Currency         string `json:"currency"`
	Interval         string `json:"interval"`
	CreditMinor      string `json:"credit_minor"`
	ChargeMinor      string `json:"charge_minor"`
	NetMinor         string `json:"net_minor"`
	NextRenewalMinor string `json:"next_renewal_minor"`
	ByProvider       bool   `json:"by_provider"`
	NextRenewalTime  string `json:"next_renewal_time,omitempty"`
}

// PreviewChange asks the provider what a change of the subscription would do, without doing it.
func PreviewChange(ctx context.Context, d Deps, tg target.Target, api payments.SubscriptionAPI, sub *plansv1.Subscription, in ChangeRequest) (Preview, error) {
	ch, err := PlanChange(ctx, d, tg, sub, in)
	if err != nil {
		return Preview{}, err
	}
	p, err := api.PreviewChange(ctx, sub.GetProviderRef(), ch.Provider)
	if err != nil {
		return Preview{}, &refuse.Provider{What: "preview change", Err: err}
	}
	out := Preview{
		Timing: previewTiming(ch.Provider.Timing), Currency: cmp.Or(p.Currency, sub.GetCurrency()), Interval: ch.Interval.String(),
		CreditMinor: strconv.FormatInt(p.CreditMinor, 10), ChargeMinor: strconv.FormatInt(p.ChargeMinor, 10), NetMinor: strconv.FormatInt(p.ResultMinor, 10),
		NextRenewalMinor: strconv.FormatInt(p.NextAmountMinor, 10), ByProvider: true,
	}
	if p.NextBilledAt != nil {
		out.NextRenewalTime = p.NextBilledAt.UTC().Format(time.RFC3339)
	}
	return out, nil
}

// previewTiming is the tenant API's word for the timing a preview was asked about: the one the request named, not always "immediately".
func previewTiming(timing string) string {
	if timing == "period_end" {
		return plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END.String()
	}
	return plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY.String()
}

// PaymentMethodLink is the provider's page where the customer updates the payment method of the subscription.
func PaymentMethodLink(ctx context.Context, api payments.SubscriptionAPI, sub *plansv1.Subscription) (string, error) {
	url, err := api.PaymentMethodLink(ctx, sub.GetProviderRef())
	if err != nil {
		return "", &refuse.Provider{What: "payment method link", Err: err}
	}
	return url, nil
}
