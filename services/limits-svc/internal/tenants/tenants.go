// Package tenants resolves the period settings (time zone, grace) of a tenant, cached for a short while.
package tenants

import (
	"context"
	"fmt"
	"sync"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// Tenant is what limits-svc needs of a tenant.
type Tenant struct {
	Location *time.Location
	Grace    time.Duration
}

// Source resolves tenants.
type Source interface {
	Tenant(ctx context.Context, id uuid.UUID) (Tenant, error)
}

// Loader fetches a tenant's current settings.
type Loader func(ctx context.Context, id uuid.UUID) (Tenant, error)

// Cache is a Source over a Loader.
type Cache struct {
	load Loader
	ttl  time.Duration

	mu    sync.Mutex
	items map[uuid.UUID]item
}

type item struct {
	t       Tenant
	expires time.Time
}

// New returns a Cache that loads tenants from plans-svc.
func New(admin plansv1.AdminServiceClient, ttl time.Duration) *Cache {
	return NewFunc(func(ctx context.Context, id uuid.UUID) (Tenant, error) {
		octx := tenant.OutgoingContext(tenant.WithOperator(ctx))
		resp, err := admin.GetTenant(octx, &plansv1.GetTenantRequest{TenantId: id.String()})
		if err != nil {
			return Tenant{}, fmt.Errorf("get tenant %s: %w", id, err)
		}
		tn := resp.GetTenant()
		loc, err := time.LoadLocation(tn.GetTimezone())
		if err != nil {
			return Tenant{}, fmt.Errorf("tenant %s has unusable time zone %q: %w", id, tn.GetTimezone(), err)
		}
		return Tenant{Location: loc, Grace: tn.GetGracePeriod().AsDuration()}, nil
	}, ttl)
}

// NewFunc returns a Cache over any loader (tests).
func NewFunc(load Loader, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &Cache{load: load, ttl: ttl, items: map[uuid.UUID]item{}}
}

// Tenant returns the tenant's current settings.
func (c *Cache) Tenant(ctx context.Context, id uuid.UUID) (Tenant, error) {
	c.mu.Lock()
	if it, ok := c.items[id]; ok && time.Now().Before(it.expires) {
		c.mu.Unlock()
		return it.t, nil
	}
	c.mu.Unlock()
	t, err := c.load(ctx, id)
	if err != nil {
		return Tenant{}, err
	}
	c.mu.Lock()
	c.items[id] = item{t: t, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return t, nil
}
