package payments

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
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
	return Capabilities{Name: m.Name(), Refunds: true}
}

// Registry maps provider names to constructors, so a worker can build the adapter a tenant's account asks for.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
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
