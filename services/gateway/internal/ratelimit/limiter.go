package ratelimit

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/otelx"
)

//go:embed gcra.lua
var gcraLua string

var gcraScript = redis.NewScript(gcraLua)

// Class is a group of routes sharing a limit.
type Class string

const (
	ClassIngest    Class = "ingest"
	ClassReadHot   Class = "read_hot"
	ClassRead      Class = "read"
	ClassWrite     Class = "write"
	ClassAnalytics Class = "analytics"
)

// The tenant tiers, the names plans-svc sends and the keys of tiers below.
const (
	TierFree       = "free"
	TierStandard   = "standard"
	TierEnterprise = "enterprise"
	// SessionTier is the tier a signed-in person's requests are limited at: a person has no tenant tier of their own.
	SessionTier = TierStandard
)

// tiers holds the limit per class for each tenant tier (docs/03-services/gateway.md#rate-limiting).
var tiers = map[string]map[Class]Limit{
	TierFree: {
		ClassIngest: {100, 500}, ClassReadHot: {50, 100}, ClassRead: {20, 40}, ClassWrite: {5, 10}, ClassAnalytics: {0.5, 5},
	},
	TierStandard: {
		ClassIngest: {2000, 10000}, ClassReadHot: {500, 1000}, ClassRead: {200, 400}, ClassWrite: {50, 100}, ClassAnalytics: {5, 20},
	},
	TierEnterprise: {
		ClassIngest: {10000, 50000}, ClassReadHot: {2000, 4000}, ClassRead: {800, 1600}, ClassWrite: {200, 400}, ClassAnalytics: {20, 50},
	},
}

// limitFor returns the limit of a tier and class. Unknown tiers get free limits.
func limitFor(tier string, class Class) Limit {
	t, ok := tiers[tier]
	if !ok {
		t = tiers[TierFree]
	}
	return t[class]
}

// Limiter charges cost units against gw:rl:{tenant}:{class} in Redis and fails open to an in-process GCRA
// (each replica gets limit ÷ replicas) when Redis errors. Ingest availability beats strict limits.
type Limiter struct {
	rdb      redis.Scripter
	replicas int
	log      *slog.Logger

	mu    sync.Mutex
	local map[string]time.Time
	now   func() time.Time

	fallback metric.Int64Counter
	rejected metric.Int64Counter
}

// NewLimiter returns a Limiter. replicas is the number of gateway replicas sharing the limit.
func NewLimiter(rdb redis.Scripter, replicas int, log *slog.Logger) (*Limiter, error) {
	if replicas < 1 {
		replicas = 1
	}
	m := otelx.NewInstruments("github.com/0furkancolak/payrudder/services/gateway/ratelimit")
	fallback := m.Counter("gateway_ratelimit_fallback", "Rate-limit decisions taken locally because Redis failed")
	rejected := m.Counter("gateway_ratelimited", "Requests rejected with 429")
	if err := m.Err(); err != nil {
		return nil, err
	}
	return &Limiter{rdb: rdb, replicas: replicas, log: log, local: map[string]time.Time{}, now: time.Now, fallback: fallback, rejected: rejected}, nil
}

// Allow charges cost units (events for ingest, 1 otherwise) for the tenant and class.
func (l *Limiter) Allow(ctx context.Context, tenantID, tier string, class Class, cost int) (Decision, error) {
	lim := limitFor(tier, class)
	if cost < 1 {
		cost = 1
	}
	key := "gw:rl:" + tenantID + ":" + string(class)
	d, err := l.redisAllow(ctx, key, lim, cost)
	if err != nil {
		l.fallback.Add(ctx, 1, metric.WithAttributes(attribute.String("class", string(class))))
		l.log.WarnContext(ctx, "rate limiter: redis unavailable, using local fallback", "error", err)
		d = l.localAllow(key, Limit{PerSecond: lim.PerSecond / float64(l.replicas), Burst: max(1, lim.Burst/l.replicas)}, cost)
	}
	if !d.Allowed {
		l.rejected.Add(ctx, 1, metric.WithAttributes(attribute.String("class", string(class)), attribute.String("tenant", tenantID)))
	}
	return d, nil
}

func (l *Limiter) redisAllow(ctx context.Context, key string, lim Limit, cost int) (Decision, error) {
	if l.rdb == nil {
		return Decision{}, fmt.Errorf("no redis")
	}
	res, err := gcraScript.Run(ctx, l.rdb, []string{key},
		lim.emission().Microseconds(), lim.Burst, cost, 0).Int64Slice()
	if err != nil {
		return Decision{}, err
	}
	if len(res) != 2 {
		return Decision{}, fmt.Errorf("gcra: unexpected reply %v", res)
	}
	return Decision{Allowed: res[0] == 1, RetryAfter: time.Duration(res[1]) * time.Microsecond}, nil
}

// localAllow is the fallback decision of one replica, whose burst is only its share of the limit. GCRA never admits a cost above the burst, which would
// refuse a large batch for ever while telling the client to retry; here such a batch costs the whole share, so it gets in when the share is full.
func (l *Limiter) localAllow(key string, lim Limit, cost int) Decision {
	cost = min(cost, lim.Burst)
	l.mu.Lock()
	defer l.mu.Unlock()
	d := step(l.local[key], l.now(), lim, cost)
	if d.Allowed {
		l.local[key] = d.TAT
	}
	return d
}

// RetryAfterSeconds renders a Retry-After header value (whole seconds, at least 1).
func RetryAfterSeconds(d time.Duration) string {
	s := int64((d + time.Second - 1) / time.Second)
	return strconv.FormatInt(max(s, 1), 10)
}
