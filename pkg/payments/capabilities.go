package payments

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"
)

// ErrUnsupported is returned when a provider is asked for something outside its Capabilities.
var ErrUnsupported = errors.New("payments: the provider does not support this operation")

// Capabilities says what a provider does for the merchant. Quotify does the rest (docs/14-billing-platform.md).
type Capabilities struct {
	// Name is the provider's registry name, e.g. "iyzico".
	Name string
	// OwnsRenewal: the provider schedules the renewals of a subscription and charges them itself.
	OwnsRenewal bool
	// OwnsDunning: the provider retries failed renewals. Quotify must not retry a payment the provider owns.
	OwnsDunning bool
	// CalculatesTax: the provider works out and collects tax.
	CalculatesTax bool
	// HostedCheckout: the customer enters the payment method on a page the provider hosts.
	HostedCheckout bool
	// Refunds: the provider has a refund API.
	Refunds bool
	// IdempotentCharges: the provider returns the first result for a repeated idempotency key instead of charging again. When
	// false, a charge command that is redelivered after the provider may have been called is answered by asking Status first.
	IdempotentCharges bool
	// Currencies the provider can charge in. Empty means any.
	Currencies []string
}

// CanCharge reports whether the provider charges in currency.
func (c Capabilities) CanCharge(currency string) bool {
	return len(c.Currencies) == 0 || slices.Contains(c.Currencies, currency)
}

// Owner says who drives renewal and dunning of a subscription sold through this provider.
func (c Capabilities) Owner() Owner {
	if c.OwnsRenewal && c.OwnsDunning {
		return OwnerProvider
	}
	return OwnerQuotify
}

// Owner of renewal and dunning.
type Owner string

const (
	OwnerQuotify  Owner = "quotify"
	OwnerProvider Owner = "provider"
)

// Provider is a PaymentProvider that declares what it does.
type Provider interface {
	PaymentProvider
	Capabilities() Capabilities
}

// Capabilities implements Provider: mock-psp is a plain charge-and-refund provider, so Quotify drives everything.
func (m *MockPSP) Capabilities() Capabilities {
	return Capabilities{Name: m.Name(), Refunds: true, IdempotentCharges: true}
}

// Registry maps provider names to constructors, so a worker can build the adapter a tenant's account asks for.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	parsers   map[string]ParseNotification
}

// Factory builds a provider from a tenant's stored account settings (credentials and options as a JSON object).
type Factory func(settings []byte) (Provider, error)

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{factories: map[string]Factory{}} }

// Register adds a provider. A name registered twice panics: it is a wiring mistake.
func (r *Registry) Register(name string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.factories[name]; dup {
		panic(fmt.Sprintf("payments: provider %q registered twice", name))
	}
	r.factories[name] = f
}

// Build returns the provider called name configured by settings, or ErrUnknownProvider.
func (r *Registry) Build(name string, settings []byte) (Provider, error) {
	r.mu.RLock()
	f, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, name)
	}
	return f(settings)
}

// Names returns the registered provider names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for n := range r.factories {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ErrUnknownProvider: no provider is registered under that name.
var ErrUnknownProvider = errors.New("payments: unknown provider")

// NotificationRef is what Quotify can read from a provider's webhook without credentials: which notification it is and which of
// our charges it is about (the idempotency key we sent as the provider's reference).
type NotificationRef struct {
	EventID        string
	IdempotencyKey string
}

// NotificationKind is what a notification is about.
type NotificationKind string

const (
	// KindCharge is the zero value: a notification that does not say otherwise is about a charge (what iyzico sends).
	KindCharge       NotificationKind = ""
	KindRefund       NotificationKind = "refund"
	KindSubscription NotificationKind = "subscription"
	// KindIgnored is an event Quotify has no use for (a transaction that was merely created, for example).
	KindIgnored NotificationKind = "ignored"
)

// Notification is what a confirmed provider notification says.
type Notification struct {
	Ref  NotificationRef
	Kind NotificationKind
	// State is the final outcome of a charge or refund: StateSucceeded or StateDeclined (a refund that did not happen).
	State       State
	ProviderRef string
	DeclineCode string
	// AmountMinor and Currency are what the provider says moved, when it says. Zero and empty otherwise.
	AmountMinor int64
	Currency    string
	// Subscription is set for KindSubscription.
	Subscription *SubscriptionEvent
	// OccurredAt is when the provider says the event happened; zero when it does not say.
	OccurredAt time.Time
}

// ErrUnverified is returned by ConfirmNotification for a notification that does not come from the provider (a bad signature). It is
// permanent: retrying the same body changes nothing.
var ErrUnverified = errors.New("payments: notification could not be verified")

// SubscriptionEvent is a provider-side change to a subscription the provider runs.
type SubscriptionEvent struct {
	ProviderRef string
	// Status is the provider's own word: active, trialing, past_due, paused, canceled.
	Status string
	// QuotifySubscriptionID is the subscription this one was created for (carried in the provider's custom data), if it says.
	QuotifySubscriptionID string
	// QuotifyMigrationID is the provider migration this subscription completes, if it says.
	QuotifyMigrationID string
	NextBilledAt       *time.Time
	// PriceRefs are the provider's ids of the prices the subscription is billed for, when the notification says.
	PriceRefs []string
}

// PendingError is returned by a charge the provider accepted but has not settled: the outcome arrives as a notification. It is not a failure,
// and it must not be retried; Quotify asks Status (which finds the notification's outcome in the ledger).
type PendingError struct{ Ref string }

func (e *PendingError) Error() string {
	return "payment accepted by the provider; the outcome arrives by notification (" + e.Ref + ")"
}

// CheckoutRequest asks a provider that hosts the checkout for a page where the customer pays and, for a provider that runs
// subscriptions, subscribes.
type CheckoutRequest struct {
	// PriceRef is the provider's id of what is sold (a catalogue price).
	PriceRef string
	Quantity int
	// QuotifySubscriptionID ties the provider's subscription to ours; the provider hands it back in its notifications.
	QuotifySubscriptionID string
	// QuotifyMigrationID ties the checkout to a provider migration that completes when the customer has subscribed.
	QuotifyMigrationID string
}

// Checkout is a hosted page.
type Checkout struct {
	URL string
	Ref string
}

// CheckoutProvider is a provider with a hosted checkout (Capabilities.HostedCheckout).
type CheckoutProvider interface {
	Provider
	CreateCheckout(ctx context.Context, r CheckoutRequest) (Checkout, error)
}

// ParseNotification reads a webhook body. It must not trust the body: it only names the notification and the charge.
type ParseNotification func(body []byte, header http.Header) (NotificationRef, error)

// Notifier is a provider that confirms a webhook notification by asking the provider what it is about, so a forged or replayed
// body cannot change anything.
type Notifier interface {
	Provider
	ConfirmNotification(ctx context.Context, body []byte, header http.Header) (Notification, error)
}

// RegisterParser registers the webhook parser of a provider.
func (r *Registry) RegisterParser(name string, p ParseNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.parsers == nil {
		r.parsers = map[string]ParseNotification{}
	}
	r.parsers[name] = p
}

// Parser returns the webhook parser of a provider.
func (r *Registry) Parser(name string) (ParseNotification, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.parsers[name]
	return p, ok
}

// SubscriptionManager is a provider that lets Quotify end a subscription the provider runs.
type SubscriptionManager interface {
	Provider
	// CancelSubscription cancels the provider's subscription ref: at the end of the period already paid for, or immediately.
	CancelSubscription(ctx context.Context, ref string, atPeriodEnd bool) error
}
