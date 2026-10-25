// Package command drives the subscriptions a provider runs: changing, pausing and resuming one, and opening the hosted checkout where a customer subscribes.
package command

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/subscriptions/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Syncer is what the commands tell plans-svc: that a subscription a provider runs now looks like this.
type Syncer interface {
	SyncProviderSubscription(ctx context.Context, tenant uuid.UUID, req *plansv1.SyncProviderSubscriptionRequest) error
}

// Deps is what the commands need. Syncer is nil only when plans-svc is not configured, in which case the query refuses first.
type Deps struct {
	Query    query.Deps
	Syncer   Syncer
	Resolver target.Resolver
	Log      *slog.Logger
}

// Applied is how a change ended.
type Applied struct {
	// Followed is set when Payrudder's copy of the subscription follows at once. Otherwise the provider's notification will make it follow.
	Followed bool
}

// ApplyChange changes the subscription at the provider and then lets Payrudder's copy follow at once. If the provider took the change and plans-svc could not
// follow, the provider's notification will make it follow, which the answer says.
func ApplyChange(ctx context.Context, d Deps, tg target.Target, api payments.SubscriptionAPI, sub *plansv1.Subscription, in query.ChangeRequest) (Applied, error) {
	ch, err := query.PlanChange(ctx, d.Query, tg, sub, in)
	if err != nil {
		return Applied{}, err
	}
	if err := api.ApplyChange(ctx, sub.GetProviderRef(), ch.Provider); err != nil {
		return Applied{}, &refuse.Provider{What: "apply change", Err: err}
	}
	err = d.Syncer.SyncProviderSubscription(ctx, tg.Tenant, &plansv1.SyncProviderSubscriptionRequest{
		SubscriptionId: sub.GetId(), Provider: tg.Provider, ProviderRef: sub.GetProviderRef(), Owner: "provider", OccurredAt: timestamppb.Now(),
		PlanVersionId: ch.PlanVersionID, Interval: ch.Interval})
	if err != nil {
		d.Log.ErrorContext(ctx, "the provider changed a subscription but plans-svc did not follow", "subscription", sub.GetId(), "err", err)
		return Applied{}, nil
	}
	return Applied{Followed: true}, nil
}

// Pause is a request to pause a subscription.
type Pause struct {
	AtPeriodEnd bool       `json:"at_period_end"`
	ResumeAt    *time.Time `json:"resume_at"`
}

// PauseSubscription asks the provider to pause the subscription. Payrudder's copy follows when the provider's notification arrives.
func PauseSubscription(ctx context.Context, api payments.SubscriptionAPI, sub *plansv1.Subscription, in Pause) error {
	if err := api.Pause(ctx, sub.GetProviderRef(), in.AtPeriodEnd, in.ResumeAt); err != nil {
		return &refuse.Provider{What: "pause", Err: err}
	}
	return nil
}

// ResumeSubscription asks the provider to resume the subscription. Payrudder's copy follows when the provider's notification arrives.
func ResumeSubscription(ctx context.Context, api payments.SubscriptionAPI, sub *plansv1.Subscription) error {
	if err := api.Resume(ctx, sub.GetProviderRef()); err != nil {
		return &refuse.Provider{What: "resume", Err: err}
	}
	return nil
}

// Checkout asks a provider with a hosted checkout for a page where a customer subscribes.
type Checkout struct {
	Provider                string `json:"provider"`
	PriceRef                string `json:"price_ref"`
	Quantity                int    `json:"quantity"`
	PayrudderSubscriptionID string `json:"quotify_subscription_id"`
}

// OpenCheckout opens the hosted checkout of a provider for a price.
func OpenCheckout(ctx context.Context, d Deps, tenant uuid.UUID, environment string, in Checkout) (payments.Checkout, error) {
	if in.Provider == "" || in.PriceRef == "" {
		return payments.Checkout{}, refuse.New(http.StatusBadRequest, "payments.checkout.fields_required", "provider and price_ref are required")
	}
	prov, err := d.Resolver.Named(ctx, tenant, in.Provider, environment)
	if err != nil {
		return payments.Checkout{}, refuse.New(http.StatusNotFound, "payments.account.not_found", "no such provider account")
	}
	cp, ok := prov.(payments.CheckoutProvider)
	if !ok {
		return payments.Checkout{}, refuse.New(http.StatusConflict, "payments.checkout.unsupported", in.Provider+" has no hosted checkout")
	}
	co, err := cp.CreateCheckout(ctx, payments.CheckoutRequest{PriceRef: in.PriceRef, Quantity: in.Quantity, PayrudderSubscriptionID: in.PayrudderSubscriptionID})
	if err != nil {
		d.Log.WarnContext(ctx, "checkout failed", "provider", in.Provider, "err", err)
		return payments.Checkout{}, refuse.ProviderFailed()
	}
	return co, nil
}
