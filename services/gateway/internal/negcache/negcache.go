// Package negcache remembers, for a short while, credentials that were looked up and found not to exist (or to be revoked), so that a stream of random guesses does not
// cost a call to plans-svc each. It is in the memory of one replica, bounded, and holds only a hash of what it was given.
package negcache

import (
	"container/list"
	"crypto/sha256"
	"sync"
	"time"
)

const (
	// DefaultTTL is how long a miss is believed. Short, so that a key or session that did not exist a moment ago and does now is not turned away for long.
	DefaultTTL = 30 * time.Second
	// DefaultMax bounds the memory: about 100 bytes an entry. The oldest guess is forgotten first.
	DefaultMax = 4096
)

// Cache is a bounded LRU of hashes with an expiry.
type Cache struct {
	mu  sync.Mutex
	ttl time.Duration
	max int
	now func() time.Time
	ll  *list.List
	m   map[[sha256.Size]byte]*list.Element
}

type entry struct {
	key     [sha256.Size]byte
	expires time.Time
}

// New returns an empty cache that believes a miss for ttl and holds at most max of them.
func New(ttl time.Duration, max int) *Cache {
	return &Cache{ttl: ttl, max: max, now: time.Now, ll: list.New(), m: map[[sha256.Size]byte]*list.Element{}}
}

// SetClock replaces the clock (tests).
func (c *Cache) SetClock(now func() time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

// Has reports whether credential was added within the last ttl.
func (c *Cache) Has(credential string) bool {
	k := sha256.Sum256([]byte(credential))
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[k]
	if !ok {
		return false
	}
	if !c.now().Before(el.Value.(*entry).expires) {
		c.ll.Remove(el)
		delete(c.m, k)
		return false
	}
	c.ll.MoveToFront(el)
	return true
}

// Add remembers credential for ttl, forgetting the least recently used one when the cache is full.
func (c *Cache) Add(credential string) {
	k := sha256.Sum256([]byte(credential))
	c.mu.Lock()
	defer c.mu.Unlock()
	expires := c.now().Add(c.ttl)
	if el, ok := c.m[k]; ok {
		el.Value.(*entry).expires = expires
		c.ll.MoveToFront(el)
		return
	}
	c.m[k] = c.ll.PushFront(&entry{key: k, expires: expires})
	for c.ll.Len() > c.max {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.m, last.Value.(*entry).key)
	}
}

// Len is the number of entries held, expired ones included until they are looked at.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
