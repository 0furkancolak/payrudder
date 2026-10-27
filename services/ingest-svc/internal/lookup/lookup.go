// Package lookup caches the two plans-svc lookups on the ingest hot path: meters by code and customers by external id.
// Hits are cached for a while, misses only briefly so a freshly created meter or customer works within seconds.
package lookup

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/cachex"
)

// Meter is what ingest needs of a meter.
type Meter struct {
	Code    string
	Version int32
	Scale   int
	// DistinctProperty is the event property a UNIQUE meter counts the distinct values of; empty for every other aggregation.
	DistinctProperty string
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

func (o Options) cachex() cachex.Options {
	return cachex.Options{Size: o.Size, HitTTL: o.HitTTL, MissTTL: o.MissTTL, LoadTimeout: o.LoadTimeout}
}

// notFound turns plans-svc's NotFound into the cache's "does not exist" answer.
func notFound(err error) error {
	if status.Code(err) == codes.NotFound {
		return cachex.ErrNotFound
	}
	return err
}

// Plans resolves meters and customers through plans-svc.
type Plans struct {
	meters    *cachex.Cache[Meter]
	customers *cachex.Cache[Customer]
}

// NewPlans returns cached lookups over client. The context of every call must carry the tenant metadata.
func NewPlans(client plansv1.PlansServiceClient, o Options) *Plans {
	o.defaults()
	return &Plans{
		meters: cachex.New(o.cachex(), func(ctx context.Context, _, code string) (Meter, error) {
			resp, err := client.GetMeter(ctx, &plansv1.GetMeterRequest{Code: code})
			if err != nil {
				return Meter{}, notFound(err)
			}
			m := resp.GetMeter()
			out := Meter{Code: m.GetCode(), Version: m.GetVersion(), Scale: int(m.GetQuantityScale())}
			if m.GetAggregation() == plansv1.Aggregation_AGGREGATION_UNIQUE {
				out.DistinctProperty = m.GetValueProperty()
			}
			return out, nil
		}),
		customers: cachex.New(o.cachex(), func(ctx context.Context, _, externalID string) (Customer, error) {
			resp, err := client.ResolveCustomer(ctx, &plansv1.ResolveCustomerRequest{ExternalId: externalID})
			if err != nil {
				return Customer{}, notFound(err)
			}
			c := resp.GetCustomer()
			return Customer{ID: c.GetId(), ExternalID: c.GetExternalId()}, nil
		}),
	}
}

// Meter returns the latest version of a meter. found=false means plans-svc answered NotFound.
func (p *Plans) Meter(ctx context.Context, tenantID, code string) (Meter, bool, error) {
	return p.meters.Get(ctx, tenantID, code)
}

// Customer resolves a customer by external id. found=false means plans-svc answered NotFound.
func (p *Plans) Customer(ctx context.Context, tenantID, externalID string) (Customer, bool, error) {
	return p.customers.Get(ctx, tenantID, externalID)
}
