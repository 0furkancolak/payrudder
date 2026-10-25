package handler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Resolver builds the provider adapter of a tenant's account from its stored, sealed settings. Adapters are cached until the
// account changes.
type Resolver struct {
	Registry *payments.Registry
	Accounts store.Accounts

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	updated  time.Time
	provider payments.Provider
}

// Named returns the adapter for a tenant's account at a provider.
func (r *Resolver) Named(ctx context.Context, tenant uuid.UUID, provider, env string) (payments.Provider, error) {
	settings, updated, err := r.Accounts.Settings(ctx, tenant, provider, env)
	if err != nil {
		return nil, fmt.Errorf("provider account %s/%s: %w", provider, env, err)
	}
	key := fmt.Sprintf("%s|%s|%s", tenant, provider, env)
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[key]; ok && c.updated.Equal(updated) {
		return c.provider, nil
	}
	p, err := r.Registry.Build(provider, settings)
	if err != nil {
		return nil, fmt.Errorf("provider account %s/%s: %w", provider, env, err)
	}
	if r.cache == nil {
		r.cache = map[string]cached{}
	}
	r.cache[key] = cached{updated: updated, provider: p}
	return p, nil
}

// Default returns the adapter of the tenant's default account for the environment, if it has one.
func (r *Resolver) Default(ctx context.Context, tenant uuid.UUID, env string) (payments.Provider, bool, error) {
	name, ok, err := r.Accounts.Default(ctx, tenant, env)
	if err != nil || !ok {
		return nil, false, err
	}
	p, err := r.Named(ctx, tenant, name, env)
	return p, err == nil, err
}
