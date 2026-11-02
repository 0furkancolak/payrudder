package health

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

var sharedFallback = counter("provider_breaker_shared_fallback", "Circuit breaker decisions taken on the replica's own state because the shared state could not be reached, by provider")

// SharedStates gives the replicas one circuit per account (Breakers.Shared).
type SharedStates interface {
	For(ref Ref) payments.SharedBreaker
}

// RedisBreakers keeps the circuits on Redis: one key per account that exists for as long as the circuit is open (its time to live is the open window, so nothing has to
// close it), and one key for the lease on the probe. A Redis that cannot be reached leaves every replica on its own breaker, counted in
// provider_breaker_shared_fallback_total, and never stops a charge.
type RedisBreakers struct {
	Client redis.Cmdable
	// Prefix starts every key ("pw:breaker:" when empty).
	Prefix string
	// Timeout bounds one call (250 ms when zero): the breaker is asked on the way to a charge.
	Timeout time.Duration
	Log     *slog.Logger

	mu     sync.Mutex
	warned time.Time
}

// For implements SharedStates.
func (r *RedisBreakers) For(ref Ref) payments.SharedBreaker {
	prefix := r.Prefix
	if prefix == "" {
		prefix = "pw:breaker:"
	}
	key := prefix + ref.Tenant.String() + ":" + ref.Provider + ":" + ref.Environment
	return &redisBreaker{r: r, provider: payments.BaseProvider(ref.Provider), open: key, probe: key + ":probe"}
}

// fallback counts a decision that fell back to the replica's own state and logs that the shared state is out of reach, at most once a minute.
func (r *RedisBreakers) fallback(provider string, err error) {
	sharedFallback.Add(context.Background(), 1, metric.WithAttributes(attribute.String("provider", provider)))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Log == nil || time.Since(r.warned) < time.Minute {
		return
	}
	r.warned = time.Now()
	r.Log.Warn("provider circuit breaker: the shared state is out of reach, this replica decides on its own", "error", err)
}

func (r *RedisBreakers) ctx() (context.Context, context.CancelFunc) {
	d := r.Timeout
	if d <= 0 {
		d = 250 * time.Millisecond
	}
	return context.WithTimeout(context.Background(), d)
}

type redisBreaker struct {
	r           *RedisBreakers
	provider    string
	open, probe string
}

// OpenFor implements payments.SharedBreaker.
func (b *redisBreaker) OpenFor() time.Duration {
	ctx, cancel := b.r.ctx()
	defer cancel()
	left, err := b.r.Client.PTTL(ctx, b.open).Result()
	if err != nil {
		b.r.fallback(b.provider, err)
		return 0
	}
	return max(left, 0) // -2: no such key, -1: no expiry (never written by this code)
}

// Opened implements payments.SharedBreaker.
func (b *redisBreaker) Opened(window time.Duration) {
	ctx, cancel := b.r.ctx()
	defer cancel()
	// shortcut: the window is whole milliseconds; a pipeline, not a transaction: a lost DEL only holds the probe for its lease.
	_, err := b.r.Client.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, b.open, uuid.NewString(), window)
		p.Del(ctx, b.probe)
		return nil
	})
	if err != nil {
		b.r.fallback(b.provider, err)
	}
}

// Closed implements payments.SharedBreaker.
func (b *redisBreaker) Closed() {
	ctx, cancel := b.r.ctx()
	defer cancel()
	if err := b.r.Client.Del(ctx, b.open, b.probe).Err(); err != nil {
		b.r.fallback(b.provider, err)
	}
}

// Probe implements payments.SharedBreaker. A Redis that cannot be reached lets the caller probe: the circuit must not stay shut for want of an answer.
func (b *redisBreaker) Probe(lease time.Duration) bool {
	ctx, cancel := b.r.ctx()
	defer cancel()
	ok, err := b.r.Client.SetNX(ctx, b.probe, uuid.NewString(), lease).Result()
	if err != nil {
		b.r.fallback(b.provider, err)
		return true
	}
	return ok
}
