// Package command moves a subscription to another payment provider (docs/14-billing-platform.md#provider-migration).
//
// To a provider that runs subscriptions itself (Paddle) the customer has to subscribe there first: a migration is started and a checkout returned. The
// old subscription is untouched until the provider confirms, so a customer who never completes the checkout costs nothing.
// To a provider Payrudder runs (iyzico) the new subscription takes over at once, or, from a provider-run subscription, at the end of the period that provider
// was paid for. The old provider is told to stop renewing first: a failure after that leaves a subscription that ends without a successor (an operator
// retries), never two that both bill.
package command

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Plans is what the commands ask of plans-svc, as the tenant.
type Plans interface {
	GetSubscription(ctx context.Context, tenant uuid.UUID, id string) (*plansv1.Subscription, error)
	StartProviderMigration(ctx context.Context, tenant uuid.UUID, req *plansv1.StartProviderMigrationRequest) (*plansv1.ProviderMigration, error)
	CompleteProviderMigration(ctx context.Context, tenant uuid.UUID, req *plansv1.CompleteProviderMigrationRequest) (*plansv1.CompleteProviderMigrationResponse, error)
	AbortProviderMigration(ctx context.Context, tenant uuid.UUID, id string) error
}

// Deps is what the commands need. Plans nil means plans-svc is not configured, and migrations are refused.
type Deps struct {
	Plans         Plans
	Resolver      target.Resolver
	Subscriptions store.Subscriptions
	Log           *slog.Logger
}

// NeedPlans refuses with 503 when plans-svc is not configured.
func (d Deps) NeedPlans() error {
	if d.Plans == nil {
		return refuse.New(http.StatusServiceUnavailable, "payments.plans.not_configured", "plans-svc is not configured")
	}
	return nil
}

// Migration states a start can end in.
const (
	// StateStarted: the customer completes a checkout at the new provider, which confirms the migration.
	StateStarted = "started"
	// StateCompleted: the new subscription has taken over.
	StateCompleted = "completed"
)

// Start asks for a subscription to move to another provider.
type Start struct {
	SubscriptionID string `json:"subscription_id"`
	ToProvider     string `json:"to_provider"`
	// PriceRef is the new provider's catalogue price; required when it runs subscriptions itself.
	PriceRef string `json:"price_ref"`
}

// Started is where a migration stands after it was started.
type Started struct {
	Migration string
	State     string
	// CheckoutURL is the page the customer completes (StateStarted).
	CheckoutURL string
	// NewSubscription and Cutover tell what took over and when (StateCompleted).
	NewSubscription string
	Cutover         time.Time
}

// StartMigration begins the migration of a subscription to another provider.
func StartMigration(ctx context.Context, d Deps, t uuid.UUID, environment string, in Start) (Started, error) {
	if err := d.NeedPlans(); err != nil {
		return Started{}, err
	}
	if in.SubscriptionID == "" || in.ToProvider == "" {
		return Started{}, refuse.New(http.StatusBadRequest, "payments.migration.fields_required", "subscription_id and to_provider are required")
	}
	sub, err := d.Plans.GetSubscription(ctx, t, in.SubscriptionID)
	if err != nil {
		return Started{}, &refuse.Plans{Err: err}
	}
	to, err := target.Resolve(ctx, d.Resolver, t, environment, in.ToProvider)
	if err != nil {
		return Started{}, err
	}
	if to.Prov.Capabilities().Owner() == payments.OwnerProvider {
		return viaCheckout(ctx, d, t, to.Prov, in)
	}
	return toPayrudder(ctx, d, t, environment, sub, in)
}

// viaCheckout starts a migration to a provider that runs subscriptions itself and returns the checkout the customer completes.
func viaCheckout(ctx context.Context, d Deps, t uuid.UUID, to payments.PaymentProvider, in Start) (Started, error) {
	cp, ok := to.(payments.CheckoutProvider)
	if !ok || in.PriceRef == "" {
		return Started{}, refuse.New(http.StatusBadRequest, "payments.migration.price_ref_required", in.ToProvider+" runs subscriptions itself: price_ref (its catalogue price) is required")
	}
	mig, err := d.Plans.StartProviderMigration(ctx, t, &plansv1.StartProviderMigrationRequest{SubscriptionId: in.SubscriptionID, ToProvider: in.ToProvider})
	if err != nil {
		return Started{}, &refuse.Plans{Err: err}
	}
	co, err := cp.CreateCheckout(ctx, payments.CheckoutRequest{PriceRef: in.PriceRef, Quantity: 1, PayrudderMigrationID: mig.GetId()})
	if err != nil {
		// No checkout, no migration: leave the subscription as it was.
		if aerr := d.Plans.AbortProviderMigration(ctx, t, mig.GetId()); aerr != nil {
			d.Log.ErrorContext(ctx, "could not abort a migration after a failed checkout", "migration", mig.GetId(), "err", aerr)
		}
		d.Log.WarnContext(ctx, "migration checkout failed", "provider", in.ToProvider, "err", err)
		return Started{}, refuse.ProviderFailed()
	}
	return Started{Migration: mig.GetId(), State: StateStarted, CheckoutURL: co.URL}, nil
}

// toPayrudder moves a subscription to a provider Payrudder runs. From a provider-run subscription, it waits for the end of the period that provider was paid
// for.
func toPayrudder(ctx context.Context, d Deps, t uuid.UUID, environment string, sub *plansv1.Subscription, in Start) (Started, error) {
	var cutover *timestamppb.Timestamp
	if sub.GetOwner() == "provider" {
		var err error
		if cutover, err = stopRenewals(ctx, d, t, environment, sub); err != nil {
			return Started{}, err
		}
	}
	mig, err := d.Plans.StartProviderMigration(ctx, t, &plansv1.StartProviderMigrationRequest{SubscriptionId: in.SubscriptionID, ToProvider: in.ToProvider, CutoverTime: cutover})
	if err != nil {
		return Started{}, &refuse.Plans{Err: err}
	}
	return Started{Migration: mig.GetId(), State: StateCompleted, NewSubscription: mig.GetNewSubscriptionId(), Cutover: mig.GetCutoverTime().AsTime()}, nil
}

// stopRenewals tells the old provider to stop renewing a subscription at the end of its paid period, which is the cutover time.
func stopRenewals(ctx context.Context, d Deps, t uuid.UUID, environment string, sub *plansv1.Subscription) (*timestamppb.Timestamp, error) {
	next, found, err := d.Subscriptions.NextBilledAt(ctx, t, sub.GetProvider(), environment, sub.GetProviderRef())
	if err != nil {
		return nil, fmt.Errorf("read the end of the paid period: %w", err)
	}
	if !found {
		return nil, refuse.New(http.StatusConflict, "payments.subscription.period_end_unknown", "the end of the paid period at "+sub.GetProvider()+" is not known yet (no notification has told us)")
	}
	old, err := target.Resolve(ctx, d.Resolver, t, environment, sub.GetProvider())
	if err != nil {
		return nil, err
	}
	m, ok := old.Prov.(payments.SubscriptionManager)
	if !ok {
		return nil, refuse.New(http.StatusConflict, "payments.subscription.cannot_cancel", sub.GetProvider()+" cannot cancel a subscription through Payrudder")
	}
	if err := m.CancelSubscription(ctx, sub.GetProviderRef(), true); err != nil {
		d.Log.WarnContext(ctx, "provider cancel failed", "provider", sub.GetProvider(), "err", err)
		return nil, refuse.ProviderFailed()
	}
	return timestamppb.New(next), nil
}

// AbortMigration gives up on a migration that has not completed.
func AbortMigration(ctx context.Context, d Deps, t uuid.UUID, migration string) error {
	if err := d.NeedPlans(); err != nil {
		return err
	}
	if err := d.Plans.AbortProviderMigration(ctx, t, migration); err != nil {
		return &refuse.Plans{Err: err}
	}
	return nil
}

// Complete is the cutover of a migration whose checkout the customer completed at the new provider: plans-svc makes the new subscription the customer's and
// ends the old one. occurred is when the provider says it happened.
func Complete(ctx context.Context, d Deps, t uuid.UUID, migrationID, providerRef string, occurred time.Time) (*plansv1.CompleteProviderMigrationResponse, error) {
	return d.Plans.CompleteProviderMigration(ctx, t, &plansv1.CompleteProviderMigrationRequest{
		MigrationId: migrationID, ProviderRef: providerRef, CutoverTime: timestamppb.New(occurred)})
}

// ReleaseSource stops the old provider from billing a subscription that has moved: a subscription the old provider ran is cancelled there.
func ReleaseSource(ctx context.Context, d Deps, t uuid.UUID, environment string, done *plansv1.CompleteProviderMigrationResponse) error {
	if done.GetSourceProviderRef() == "" {
		return nil // Payrudder ran the old subscription: it ended with the cutover
	}
	prov, err := d.Resolver.Named(ctx, t, done.GetSourceProvider(), environment)
	if err != nil {
		return err
	}
	m, ok := prov.(payments.SubscriptionManager)
	if !ok {
		return fmt.Errorf("%s cannot cancel a subscription: release %s there by hand", done.GetSourceProvider(), done.GetSourceProviderRef())
	}
	// Immediately: the customer now pays the new provider, and one more renewal here would be a second bill.
	return m.CancelSubscription(ctx, done.GetSourceProviderRef(), false)
}
