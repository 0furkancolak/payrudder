// Package meters resolves meter definitions from plans-svc for the consumers of usage events (aggregator, limits-svc),
// cached briefly and bounded (pkg/cachex).
package meters

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/cachex"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/usage"
)

// Source resolves meters.
type Source interface {
	// Get returns the meter, the service's not-found error if it does not exist, or the transport error.
	Get(ctx context.Context, tenantID uuid.UUID, code string) (usage.Meter, error)
}

// Cache resolves meters through plans-svc (the latest version of a code), caching hits for the TTL.
type Cache struct {
	c        *cachex.Cache[usage.Meter]
	notFound error
}

// New returns a cache over client. A meter plans-svc does not know is reported as notFound, the service's own error (and
// error key); ttl <= 0 means one minute.
func New(client plansv1.PlansServiceClient, ttl time.Duration, notFound error) *Cache {
	return &Cache{notFound: notFound, c: cachex.New(cachex.Options{HitTTL: ttl}, func(ctx context.Context, scope, code string) (usage.Meter, error) {
		tid, err := uuid.Parse(scope)
		if err != nil {
			return usage.Meter{}, err
		}
		ctx = tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: tid}))
		resp, err := client.GetMeter(ctx, &plansv1.GetMeterRequest{Code: code})
		if status.Code(err) == codes.NotFound {
			return usage.Meter{}, cachex.ErrNotFound
		}
		if err != nil {
			return usage.Meter{}, err
		}
		return usage.FromProto(resp.GetMeter()), nil
	})}
}

// Get implements Source.
func (c *Cache) Get(ctx context.Context, tenantID uuid.UUID, code string) (usage.Meter, error) {
	m, found, err := c.c.Get(ctx, tenantID.String(), code)
	if err != nil {
		return usage.Meter{}, err
	}
	if !found {
		return usage.Meter{}, c.notFound
	}
	return m, nil
}
