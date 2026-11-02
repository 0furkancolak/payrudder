package delivery

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Limiter is a token bucket per tenant: it caps how fast the platform's sender sends one tenant's mail, so that a tenant that loops or abuses it cannot spend the
// sending domain's reputation (plan 11). A mail over the limit is not dropped: Deliver answers ErrRateLimited and the caller's own retry sends it later.
//
// shortcut: the buckets live in this process, so with N replicas a tenant may send up to N times the rate; and the map is emptied when it holds maxCached
// tenants, which gives each a fresh burst. Move the buckets to Redis if either matters.
type Limiter struct {
	// PerMinute is the sustained rate; 0 switches the limit off.
	PerMinute int
	// Burst is the bucket's size: how many mails a tenant may send at once after being idle (at least 1).
	Burst int
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	mu      sync.Mutex
	buckets map[uuid.UUID]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

// Allow takes one token for the tenant and reports whether there was one.
func (l *Limiter) Allow(tenantID uuid.UUID) bool {
	if l == nil || l.PerMinute <= 0 {
		return true
	}
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
