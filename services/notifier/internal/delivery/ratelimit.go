package delivery

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

var rateFallbackTotal = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/payrudder/services/notifier").Int64Counter("notifier_mail_rate_limit_fallback",
		metric.WithDescription("Mail rate-limit decisions taken on the replica's own buckets because the shared ones could not be reached"))
	if err != nil {
		panic(err)
	}
	return c
}()

// Limiter is a token bucket per tenant: it caps how fast the platform's sender sends one tenant's mail, so that a tenant that loops or abuses it cannot spend the
// sending domain's reputation (plan 11). A mail over the limit is not dropped: Deliver answers ErrRateLimited and the caller's own retry sends it later.
//
// With Shared set (Redis, NOTIFIER_REDIS_URL) every replica draws from one bucket per tenant, so the rate is the platform's, not N times it. Without it, or when the
// shared buckets cannot be reached, a replica decides on buckets of its own (the rate is then per replica, counted in notifier_mail_rate_limit_fallback_total, and
// the limit never stops mail because Redis is down).
//
// shortcut: the local buckets live in this process, and the map is emptied when it holds maxCached tenants, which gives each a fresh burst.
type Limiter struct {
	// PerMinute is the sustained rate; 0 switches the limit off.
	PerMinute int
	// Burst is the bucket's size: how many mails a tenant may send at once after being idle (at least 1).
	Burst int
	// Now is the clock of the local buckets; time.Now when nil.
	Now func() time.Time
	// Shared holds the buckets of every replica; nil keeps them local.
	Shared Shared
	Log    *slog.Logger

	mu      sync.Mutex
	buckets map[uuid.UUID]*bucket
	warned  time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

// Allow takes one token for the tenant and reports whether there was one.
func (l *Limiter) Allow(ctx context.Context, tenantID uuid.UUID) bool {
	if l == nil || l.PerMinute <= 0 {
		return true
	}
	if l.Shared != nil {
		ok, err := l.Shared.Take(ctx, tenantID, l.PerMinute, max(l.Burst, 1))
		if err == nil {
			return ok
		}
		rateFallbackTotal.Add(ctx, 1)
		l.warnShared(ctx, err)
	}
	return l.allowLocal(tenantID)
}

// warnShared logs that the shared buckets are out of reach, at most once a minute.
func (l *Limiter) warnShared(ctx context.Context, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Log == nil || time.Since(l.warned) < time.Minute {
		return
	}
	l.warned = time.Now()
	l.Log.WarnContext(ctx, "mail rate limit: the shared buckets are out of reach, this replica decides on its own", "error", err)
}

func (l *Limiter) allowLocal(tenantID uuid.UUID) bool {
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	burst := float64(max(l.Burst, 1))
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil || len(l.buckets) >= maxCached {
		l.buckets = make(map[uuid.UUID]*bucket)
	}
	t := now()
	b, ok := l.buckets[tenantID]
	if !ok {
		b = &bucket{tokens: burst, at: t}
		l.buckets[tenantID] = b
	}
	b.tokens = min(burst, b.tokens+t.Sub(b.at).Seconds()*float64(l.PerMinute)/60)
	b.at = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
