//go:build integration

package itest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
)

// The mail rate limit on a real Redis: two replicas draw from one bucket per tenant, another tenant has its own, and a Redis that cannot be reached leaves each replica
// on its own bucket instead of refusing mail.
func TestMailRateLimitIsSharedThroughRedis(t *testing.T) {
	ctx := context.Background()
	opts, err := redis.ParseURL(testkit.StartRedis(t))
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()
	// A burst of 5 and a rate so slow (one an hour) that nothing refills during the test.
	replica := func() *delivery.Limiter {
		return &delivery.Limiter{PerMinute: 1, Burst: 5, Shared: delivery.RedisBuckets{Client: rdb, Prefix: "nf:test:" + t.Name() + ":"}}
	}
	a, b := replica(), replica()
	acme, other := uuid.New(), uuid.New()
	sent := 0
	for i := range 12 {
		l := a
		if i%2 == 1 {
			l = b
		}
		if l.Allow(ctx, acme) {
			sent++
		}
	}
	if sent != 5 {
		t.Fatalf("two replicas sent %d mails of a shared burst of 5", sent)
	}
	if !a.Allow(ctx, other) || !b.Allow(ctx, other) {
		t.Error("one tenant's flood stopped another tenant")
	}

	// Redis is gone: each replica keeps a burst of its own, and mail is not refused for want of an answer.
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer func() { _ = dead.Close() }()
	c := &delivery.Limiter{PerMinute: 1, Burst: 2, Shared: delivery.RedisBuckets{Client: dead}}
	if !c.Allow(ctx, acme) || !c.Allow(ctx, acme) || c.Allow(ctx, acme) {
		t.Error("with Redis down the replica should fall back to a burst of 2 of its own")
	}
}
