package httpapi

import (
	"sync"
	"time"
)

type dashCache struct {
	mu sync.Mutex
	m  map[string]dashCacheEntry
}

type dashCacheEntry struct {
	at   time.Time
	body []byte
}

func (c *dashCache) get(key string, now time.Time) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.Sub(e.at) > dashCacheTTL {
		return nil, false
	}
	return e.body, true
}

func (c *dashCache) set(key string, now time.Time, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]dashCacheEntry{}
	}
	if len(c.m) >= dashCacheEntries {
		for k, e := range c.m {
			if now.Sub(e.at) > dashCacheTTL {
				delete(c.m, k)
			}
		}
		for k := range c.m {
			if len(c.m) < dashCacheEntries {
				break
			}
			delete(c.m, k)
		}
	}
	c.m[key] = dashCacheEntry{at: now, body: body}
}
