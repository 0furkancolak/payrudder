package einvoice

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrUnknownProvider: no provider is registered under that name.
var ErrUnknownProvider = errors.New("einvoice: unknown provider")

// Factory builds a provider from a tenant's stored account settings (a JSON object holding credentials and options).
type Factory func(settings []byte) (Provider, error)

// Registry maps provider names to constructors, like payments.Registry.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{factories: map[string]Factory{}} }

// Register adds a provider. A name registered twice panics: it is a wiring mistake.
func (r *Registry) Register(name string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.factories[name]; dup {
		panic(fmt.Sprintf("einvoice: provider %q registered twice", name))
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

// Has reports whether name is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[name]
	return ok
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
