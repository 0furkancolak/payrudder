package delivery

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLimiterIsPerTenantAndRefills(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	l := &Limiter{PerMinute: 60, Burst: 3, Now: func() time.Time { return now }}
	a, b := uuid.New(), uuid.New()
	for i := 0; i < 3; i++ {
		if !l.Allow(ctx, a) {
			t.Fatalf("mail %d of the burst was refused", i+1)
		}
	}
	if l.Allow(ctx, a) {
		t.Error("a fourth mail at once passed a burst of 3")
	}
	if !l.Allow(ctx, b) {
		t.Error("one tenant's flood stopped another tenant")
	}
	now = now.Add(2 * time.Second) // 60 a minute is one a second
	first, second, third := l.Allow(ctx, a), l.Allow(ctx, a), l.Allow(ctx, a)
	if !first || !second || third {
		t.Error("two seconds should refill exactly two tokens")
	}
	now = now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		l.Allow(ctx, a)
	}
	if l.Allow(ctx, a) {
		t.Error("the bucket must not hold more than the burst")
	}
}

func TestLimiterOffWhenZero(t *testing.T) {
	ctx := context.Background()
	var none *Limiter
	off := &Limiter{PerMinute: 0, Burst: 1}
	id := uuid.New()
	for i := 0; i < 100; i++ {
		if !none.Allow(ctx, id) || !off.Allow(ctx, id) {
			t.Fatal("a limit of 0 (or none) limits nothing")
		}
	}
}

// sharedFake is the shared buckets: one count for every replica, or an outage.
type sharedFake struct {
	left atomic.Int32
	err  error
}

func (s *sharedFake) Take(context.Context, uuid.UUID, int, int) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.left.Add(-1) >= 0, nil
}

// Two replicas that share the buckets share the rate: what one takes the other does not have. When the shared buckets cannot be reached each decides on its own, and the
// limit never refuses mail because Redis is down.
func TestLimiterSharesBucketsBetweenReplicasAndFallsBackLocally(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	shared := &sharedFake{}
	shared.left.Store(3)
	one, two := &Limiter{PerMinute: 60, Burst: 3, Shared: shared}, &Limiter{PerMinute: 60, Burst: 3, Shared: shared}
	got := 0
	for _, l := range []*Limiter{one, two, one, two, one, two} {
		if l.Allow(ctx, id) {
			got++
		}
	}
	if got != 3 {
		t.Fatalf("two replicas sent %d mails of a shared burst of 3", got)
	}

	down := &sharedFake{err: errors.New("redis: connection refused")}
	a, b := &Limiter{PerMinute: 60, Burst: 2, Shared: down}, &Limiter{PerMinute: 60, Burst: 2, Shared: down}
	for _, l := range []*Limiter{a, b} {
		first, second, third := l.Allow(ctx, id), l.Allow(ctx, id), l.Allow(ctx, id)
		if !first || !second || third {
			t.Fatal("with the shared buckets down each replica should keep a burst of 2 of its own")
		}
	}
}
