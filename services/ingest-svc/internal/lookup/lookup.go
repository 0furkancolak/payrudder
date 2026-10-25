// Package lookup caches the two plans-svc lookups on the ingest hot path: meters by code and customers by external id.
// Hits are cached for a while, misses only briefly so a freshly created meter or customer works within seconds.
package lookup

import (
	"context"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// Meter is what ingest needs of a meter.
type Meter struct {
	Code    string
	Version int32
	Scale   int
}

// Customer is what ingest needs of a customer.
type Customer struct {
	ID         string
	ExternalID string
}

// Options tune the caches. Zero values take the defaults noted.
type Options struct {
	Size        int           // default 10000 entries per cache
	HitTTL      time.Duration // default 30s
	MissTTL     time.Duration // default 2s
	LoadTimeout time.Duration // default 2s
}

func (o *Options) defaults() {
	if o.Size <= 0 {
		o.Size = 10000
	}
	if o.HitTTL <= 0 {
		o.HitTTL = 30 * time.Second
	}
	if o.MissTTL <= 0 {
		o.MissTTL = 2 * time.Second
	}
	if o.LoadTimeout <= 0 {
		o.LoadTimeout = 2 * time.Second
	}
}

type cache[V any] struct {
	hits    *expirable.LRU[string, V]
	misses  *expirable.LRU[string, struct{}]
	flight  singleflight.Group
	load    func(ctx context.Context, key string) (V, error)
	timeout time.Duration
}

func newCache[V any](o Options, load func(ctx context.Context, key string) (V, error)) *cache[V] {
	return &cache[V]{
		hits:    expirable.NewLRU[string, V](o.Size, nil, o.HitTTL),
		misses:  expirable.NewLRU[string, struct{}](o.Size, nil, o.MissTTL),
		load:    load,
		timeout: o.LoadTimeout,
	}
}

// get returns (value, found, err). tenantID scopes the cache key; ctx must carry the tenant metadata for the load.
func (c *cache[V]) get(ctx context.Context, tenantID, key string) (V, bool, error) {
	var zero V
	ck := tenantID + "/" + key
	if v, ok := c.hits.Get(ck); ok {
		return v, true, nil
	}
	if _, ok := c.misses.Get(ck); ok {
		return zero, false, nil
	}
	res, err, _ := c.flight.Do(ck, func() (any, error) {
		// Detached from the first caller's cancellation: the result is shared with every waiter.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()
		v, err := c.load(lctx, key)
		if status.Code(err) == codes.NotFound {
			c.misses.Add(ck, struct{}{})
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		c.hits.Add(ck, v)
		return v, nil
	})
	if err != nil {
		return zero, false, err
	}
	if res == nil {
		return zero, false, nil
	}
	return res.(V), true, nil
}

// Plans resolves meters and customers through plans-svc.
type Plans struct {
	meters    *cache[Meter]
	customers *cache[Customer]
}

// NewPlans returns cached lookups over client. The context of every call must carry the tenant metadata.
func NewPlans(client plansv1.PlansServiceClient, o Options) *Plans {
	o.defaults()
	return &Plans{
		meters: newCache(o, func(ctx context.Context, code string) (Meter, error) {
			resp, err := client.GetMeter(ctx, &plansv1.GetMeterRequest{Code: code})
			if err != nil {
				return Meter{}, err
			}
			m := resp.GetMeter()
			return Meter{Code: m.GetCode(), Version: m.GetVersion(), Scale: int(m.GetQuantityScale())}, nil
		}),
		customers: newCache(o, func(ctx context.Context, externalID string) (Customer, error) {
			resp, err := client.ResolveCustomer(ctx, &plansv1.ResolveCustomerRequest{ExternalId: externalID})
			if err != nil {
				return Customer{}, err
			}
			c := resp.GetCustomer()
			return Customer{ID: c.GetId(), ExternalID: c.GetExternalId()}, nil
		}),
	}
}

// Meter returns the latest version of a meter. found=false means plans-svc answered NotFound.
func (p *Plans) Meter(ctx context.Context, tenantID, code string) (Meter, bool, error) {
	return p.meters.get(ctx, tenantID, code)
}

// Customer resolves a customer by external id. found=false means plans-svc answered NotFound.
func (p *Plans) Customer(ctx context.Context, tenantID, externalID string) (Customer, bool, error) {
	return p.customers.get(ctx, tenantID, externalID)
}
