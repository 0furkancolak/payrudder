// Package target is the tenant, environment and provider account an operator call about a provider is for, and the adapter of that account.
package target

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
)

// Resolver builds the adapter of a tenant's account at a provider (handler.Resolver).
type Resolver interface {
	Named(ctx context.Context, tenant uuid.UUID, provider, env string) (payments.Provider, error)
}

// Target is what a call is about.
type Target struct {
	Tenant   uuid.UUID
	Env      string
	Provider string
	Prov     payments.Provider
}

// Resolve finds the adapter of the tenant's account at the provider, or refuses with 404 when it has none that works.
func Resolve(ctx context.Context, res Resolver, tenant uuid.UUID, env, provider string) (Target, error) {
	prov, err := res.Named(ctx, tenant, provider, env)
	if err != nil {
		return Target{}, refuse.New(http.StatusNotFound, errkey.Key("payments.account.not_found"), "no account at "+provider+" for this tenant")
	}
	return Target{Tenant: tenant, Env: env, Provider: provider, Prov: prov}, nil
}
