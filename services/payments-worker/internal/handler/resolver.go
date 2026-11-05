package handler

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/health"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Resolver builds the provider adapter of a tenant's account from its stored, sealed settings. Adapters are cached until the
// account changes.
type Resolver struct {
	Registry *payments.Registry
	Accounts store.Accounts
	// Shadow, when set, compares each account read with the same account in the new tables (ADR 0026, step 3) and changes nothing about what is returned.
	Shadow *store.Shadow

	mu    sync.Mutex
	cache map[string]cached
	// accounts remembers whose account each adapter in the cache was built for, so that a charge made through an adapter can be counted against that account's
	// circuit breaker and recorded under its key.
	accounts map[payments.Provider]health.Ref
}

type cached struct {
	updated  time.Time
	provider payments.Provider
}

// Account returns the account an adapter was built for by Named (or Default), if it is one of them.
func (r *Resolver) Account(p payments.PaymentProvider) (health.Ref, bool) {
	prov, ok := p.(payments.Provider)
	if !ok || !reflect.TypeOf(prov).Comparable() {
		return health.Ref{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ref, ok := r.accounts[prov]
	return ref, ok
}

// Named returns the adapter for a tenant's account at a provider.
func (r *Resolver) Named(ctx context.Context, tenant uuid.UUID, provider, env string) (payments.Provider, error) {
	settings, updated, err := r.Accounts.Settings(ctx, tenant, provider, env)
	if err != nil {
		return nil, fmt.Errorf("provider account %s/%s: %w", provider, env, err)
	}
	settings = r.Shadow.Read(ctx, tenant, store.KindPayment, provider, env, settings)
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
	// An account stored in one mode that points at the provider's other mode is never used (it may predate the check at the door).
	if err := payments.CheckLivemode(p, env); err != nil {
		return nil, fmt.Errorf("provider account %s/%s: %w", provider, env, err)
	}
	if r.cache == nil {
		r.cache, r.accounts = map[string]cached{}, map[payments.Provider]health.Ref{}
	}
	if old, ok := r.cache[key]; ok && reflect.TypeOf(old.provider).Comparable() {
		delete(r.accounts, old.provider) // the account changed: its old adapter is no one's now
	}
	r.cache[key] = cached{updated: updated, provider: p}
	if reflect.TypeOf(p).Comparable() { // an adapter is a pointer; one that is not cannot be a map key and has no breaker
		r.accounts[p] = health.Ref{Tenant: tenant, Provider: provider, Environment: env}
	}
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
