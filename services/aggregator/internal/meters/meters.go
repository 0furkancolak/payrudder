// Package meters resolves meter definitions from plans-svc, cached briefly.
package meters

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/usage"
)

// ErrNotFound is returned for a meter plans-svc doesn't know.
var ErrNotFound = errkey.New(codes.NotFound, "aggregator.meter.not_found", "meter not found")

// Cache resolves meters through plans-svc (latest version of a code), caching hits for TTL.
type Cache struct {
	client plansv1.PlansServiceClient
	hits   *expirable.LRU[string, usage.Meter]
	flight singleflight.Group
}

// New returns a cache over client.
func New(client plansv1.PlansServiceClient, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &Cache{client: client, hits: expirable.NewLRU[string, usage.Meter](10000, nil, ttl)}
}

// Get returns the meter, ErrNotFound if it doesn't exist, or the transport error.
func (c *Cache) Get(ctx context.Context, tenantID uuid.UUID, code string) (usage.Meter, error) {
	key := tenantID.String() + "/" + code
	if m, ok := c.hits.Get(key); ok {
		return m, nil
	}
	res, err, _ := c.flight.Do(key, func() (any, error) {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		lctx = tenant.OutgoingContext(tenant.NewContext(lctx, tenant.Info{ID: tenantID}))
		resp, err := c.client.GetMeter(lctx, &plansv1.GetMeterRequest{Code: code})
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		m := usage.FromProto(resp.GetMeter())
		c.hits.Add(key, m)
		return m, nil
	})
	if err != nil {
		return usage.Meter{}, err
	}
	return res.(usage.Meter), nil
}
