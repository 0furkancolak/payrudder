// Package tenantinfo resolves the period settings of a tenant (time zone, grace) from plans-svc for services that classify
// usage (aggregator, limits-svc), cached briefly and bounded (pkg/cachex): concurrent misses for one tenant share a load.
package tenantinfo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/cachex"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // tenant time zones must load in distroless images
)

// Settings is what a usage consumer needs of a tenant.
type Settings struct {
	Timezone string
	Location *time.Location
	Grace    time.Duration
}

// Source resolves tenants.
type Source interface {
	Tenant(ctx context.Context, id uuid.UUID) (Settings, error)
}

// Loader fetches a tenant's current settings.
type Loader func(ctx context.Context, id uuid.UUID) (Settings, error)

// Cache is a Source over a Loader, caching for a TTL.
type Cache struct{ c *cachex.Cache[Settings] }

// New returns a Cache that loads tenants from plans-svc as the operator; ttl <= 0 means one minute. A tenant with an
// unusable time zone is an error, never a silent UTC.
func New(admin plansv1.AdminServiceClient, ttl time.Duration) *Cache {
	return NewFunc(func(ctx context.Context, id uuid.UUID) (Settings, error) {
		resp, err := admin.GetTenant(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetTenantRequest{TenantId: id.String()})
		if err != nil {
			return Settings{}, fmt.Errorf("get tenant %s: %w", id, err)
		}
		tn := resp.GetTenant()
		loc, err := time.LoadLocation(tn.GetTimezone())
		if err != nil {
			return Settings{}, fmt.Errorf("tenant %s has unusable time zone %q: %w", id, tn.GetTimezone(), err)
		}
		return Settings{Timezone: tn.GetTimezone(), Location: loc, Grace: tn.GetGracePeriod().AsDuration()}, nil
	}, ttl)
}

// NewFunc returns a Cache over any loader (tests).
func NewFunc(load Loader, ttl time.Duration) *Cache {
	return &Cache{c: cachex.New(cachex.Options{HitTTL: ttl, LoadTimeout: 5 * time.Second}, func(ctx context.Context, _, key string) (Settings, error) {
		id, err := uuid.Parse(key)
		if err != nil {
			return Settings{}, err
		}
		return load(ctx, id)
	})}
}

// Tenant returns the tenant's current settings.
func (c *Cache) Tenant(ctx context.Context, id uuid.UUID) (Settings, error) {
	s, _, err := c.c.Get(ctx, "", id.String())
	return s, err
}
