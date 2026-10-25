// Package cachex is the read-through cache for lookups on a hot path (meters, customers, tenant settings): a bounded LRU
// with a time to live, one load in flight per key whatever the number of waiting callers, and an optional short memory of
// "does not exist" answers so a missing key is not asked for again on every event.
package cachex

import (
	"context"
	"errors"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"golang.org/x/sync/singleflight"
)

// ErrNotFound is what a Loader returns (itself or wrapped) when the key does not exist. It is an answer, not a failure:
// Get reports found=false and, when MissTTL is set, remembers it.
var ErrNotFound = errors.New("cachex: not found")

// Options tune a Cache. Zero values take the defaults noted.
type Options struct {
	Size        int           // entries per cache, default 10000
	HitTTL      time.Duration // how long a loaded value is served, default 1 minute
	MissTTL     time.Duration // how long a not-found answer is served; 0 does not remember misses
	LoadTimeout time.Duration // bound of one load, default 3 seconds
}

// Loader fetches one value. scope separates keys of different owners (a tenant id) and key names the value; ctx is the
// caller's, detached from its cancellation and bounded by LoadTimeout, so it still carries the caller's values (the
// tenant metadata of the outgoing call).
type Loader[V any] func(ctx context.Context, scope, key string) (V, error)

// Cache is a read-through cache. The zero value is not usable; use New.
type Cache[V any] struct {
	hits    *expirable.LRU[string, V]
	misses  *expirable.LRU[string, struct{}]
	flight  singleflight.Group
	load    Loader[V]
	timeout time.Duration
}

type result[V any] struct {
	v  V
	ok bool
}

// New returns a Cache over load.
func New[V any](o Options, load Loader[V]) *Cache[V] {
	if o.Size <= 0 {
		o.Size = 10000
	}
	if o.HitTTL <= 0 {
		o.HitTTL = time.Minute
	}
	if o.LoadTimeout <= 0 {
		o.LoadTimeout = 3 * time.Second
	}
	c := &Cache[V]{hits: expirable.NewLRU[string, V](o.Size, nil, o.HitTTL), load: load, timeout: o.LoadTimeout}
	if o.MissTTL > 0 {
		c.misses = expirable.NewLRU[string, struct{}](o.Size, nil, o.MissTTL)
	}
	return c
}

// Get returns the value of key in scope. found=false means the loader answered ErrNotFound; any other loader error is
// returned and not cached.
func (c *Cache[V]) Get(ctx context.Context, scope, key string) (v V, found bool, err error) {
	ck := scope + "/" + key
	if v, ok := c.hits.Get(ck); ok {
		return v, true, nil
	}
	if c.misses != nil {
		if _, ok := c.misses.Get(ck); ok {
			return v, false, nil
		}
	}
	res, err, _ := c.flight.Do(ck, func() (any, error) {
		// Detached from the first caller's cancellation: the result is shared with every waiter.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()
		val, err := c.load(lctx, scope, key)
		if errors.Is(err, ErrNotFound) {
			if c.misses != nil {
				c.misses.Add(ck, struct{}{})
			}
			return result[V]{}, nil
		}
		if err != nil {
			return nil, err
		}
		c.hits.Add(ck, val)
		return result[V]{val, true}, nil
	})
	if err != nil {
		return v, false, err
	}
	r := res.(result[V])
	return r.v, r.ok, nil
}
