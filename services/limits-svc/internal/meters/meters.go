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

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/limits-svc/internal/domain"
)

// ErrNotFound is returned for a meter plans-svc doesn't know.
var ErrNotFound = status.Error(codes.NotFound, "meter not found")

// Source resolves meters.
type Source interface {
	Get(ctx context.Context, tenantID uuid.UUID, code string) (domain.Meter, error)
}

// Cache resolves meters through plans-svc (latest version of a code), caching hits for TTL.
type Cache struct {
	client plansv1.PlansServiceClient
	hits   *expirable.LRU[string, domain.Meter]
	flight singleflight.Group
}

// New returns a cache over client.
func New(client plansv1.PlansServiceClient, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &Cache{client: client, hits: expirable.NewLRU[string, domain.Meter](10000, nil, ttl)}
}

// Get returns the meter, ErrNotFound if it doesn't exist, or the transport error.
func (c *Cache) Get(ctx context.Context, tenantID uuid.UUID, code string) (domain.Meter, error) {
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
		m := toDomain(resp.GetMeter())
		c.hits.Add(key, m)
		return m, nil
	})
	if err != nil {
		return domain.Meter{}, err
	}
	return res.(domain.Meter), nil
}

func toDomain(m *plansv1.Meter) domain.Meter {
	out := domain.Meter{Code: m.GetCode(), Scale: int(m.GetQuantityScale())}
	switch m.GetAggregation() {
	case plansv1.Aggregation_AGGREGATION_COUNT:
		out.Aggregation = domain.Count
	case plansv1.Aggregation_AGGREGATION_MAX:
		out.Aggregation = domain.Max
	default:
		out.Aggregation = domain.Sum
	}
	if len(m.GetFilter()) > 0 {
		out.Filter = make(map[string][]string, len(m.GetFilter()))
		for k, v := range m.GetFilter() {
			out.Filter[k] = v.GetValues()
		}
	}
	return out
}
