//go:build integration

package itest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	accounthealth "github.com/0furkancolak/payrudder/services/payments-worker/internal/health"
)

// The circuit breaker on a real Redis: an outage one replica found makes the other refuse for the open window with no failures of its own, another account is not
// touched, the window ends by itself (the key's time to live), a good probe frees the circuit, and a Redis that cannot be reached leaves each replica on its own count.
func TestCircuitBreakerIsSharedThroughRedis(t *testing.T) {
	opts, err := redis.ParseURL(testkit.StartRedis(t))
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()
	const window = 2 * time.Second
	outage := &payments.TransientError{Err: errors.New("503")}
	replica := func(c redis.Cmdable) *accounthealth.Breakers {
		return &accounthealth.Breakers{Settings: payments.BreakerSettings{Failures: 2, OpenFor: window}, Shared: &accounthealth.RedisBreakers{Client: c, Prefix: "pw:test:" + t.Name() + ":"}}
	}
	acme := accounthealth.Ref{Tenant: uuid.New(), Provider: "stripe~eu", Environment: "live"}
	other := accounthealth.Ref{Tenant: uuid.New(), Provider: "stripe", Environment: "live"}
	a, b := replica(rdb), replica(rdb)

	for range 2 {
		done, err := a.Allow(acme)
		if err != nil {
			t.Fatal(err)
		}
		done(outage)
	}
	if a.For(acme).State() != payments.BreakerOpen || b.For(acme).State() != payments.BreakerClosed {
		t.Fatalf("a = %s, b = %s: only a saw the failures", a.For(acme).State(), b.For(acme).State())
	}
	_, err = b.Allow(acme)
	var tr *payments.TransientError
	if !errors.Is(err, payments.ErrCircuitOpen) || !errors.As(err, &tr) || tr.RetryAfter <= 0 || tr.RetryAfter > window {
		t.Fatalf("b while a has the circuit open: %v, want refused with at most the window left", err)
	}
	if done, err := b.Allow(other); err != nil {
		t.Errorf("another account of another tenant: %v", err)
	} else {
		done(nil)
	}

	// The window is the key's time to live: nobody closes it.
	time.Sleep(window + 200*time.Millisecond)
	done, err := b.Allow(acme)
	if err != nil {
		t.Fatalf("b after the window: %v", err)
	}
	done(nil)

	// a is half-open and probes; while that probe is out the lease is taken, and a good probe frees the circuit for everyone.
	probe, err := a.Allow(acme)
	if err != nil {
		t.Fatalf("a's probe: %v", err)
	}
	probe(nil)
	if a.For(acme).State() != payments.BreakerClosed {
		t.Errorf("a after a good probe: %s", a.For(acme).State())
	}
	if n, err := rdb.Exists(t.Context(), "pw:test:"+t.Name()+":"+acme.Tenant.String()+":stripe~eu:live").Result(); err != nil || n != 0 {
		t.Errorf("the open key after the probe: %d, %v, want none", n, err)
	}

	// Redis is gone: nothing is shared and nothing is refused for want of an answer; each replica needs its own failures.
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer func() { _ = dead.Close() }()
	c, d := replica(dead), replica(dead)
	for range 2 {
		done, err := c.Allow(acme)
		if err != nil {
			t.Fatalf("with Redis down: %v", err)
		}
		done(outage)
	}
	if c.For(acme).State() != payments.BreakerOpen {
		t.Errorf("c on its own count: %s", c.For(acme).State())
	}
	if done, err := d.Allow(acme); err != nil {
		t.Errorf("d with Redis down should know nothing of c's circuit: %v", err)
	} else {
		done(nil)
	}
}
