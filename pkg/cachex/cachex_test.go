package cachex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type ctxKey struct{}

func TestHitIsServedFromTheCache(t *testing.T) {
	var calls atomic.Int32
	c := New(Options{}, func(_ context.Context, scope, key string) (string, error) {
		calls.Add(1)
		return scope + ":" + key, nil
	})
	for range 3 {
		v, ok, err := c.Get(context.Background(), "t1", "k")
		if err != nil || !ok || v != "t1:k" {
			t.Fatalf("Get = %q %v %v", v, ok, err)
		}
	}
	if _, _, err := c.Get(context.Background(), "t2", "k"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("loads = %d, want 2 (one per scope)", calls.Load())
	}
}

func TestConcurrentMissesShareOneLoadAndSurviveCancellation(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c := New(Options{}, func(ctx context.Context, _, _ string) (int, error) {
		calls.Add(1)
		<-release
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if ctx.Value(ctxKey{}) != "v" {
			return 0, errors.New("caller's context values were lost")
		}
		return 7, nil
	})
	first, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "v"))
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		ctx := context.WithValue(context.Background(), ctxKey{}, "v")
		if i == 0 {
			ctx = first
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, ok, err := c.Get(ctx, "s", "k")
			if err != nil || !ok || v != 7 {
				errs <- fmt.Errorf("Get = %d %v: %w", v, ok, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	cancel() // the caller that started the load goes away; the others must still get the value
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("loads = %d, want 1", calls.Load())
	}
}

func TestErrorsAreNotCached(t *testing.T) {
	var calls atomic.Int32
	boom := errors.New("boom")
	c := New(Options{}, func(context.Context, string, string) (int, error) {
		if calls.Add(1) == 1 {
			return 0, boom
		}
		return 1, nil
	})
	if _, _, err := c.Get(context.Background(), "s", "k"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if v, ok, err := c.Get(context.Background(), "s", "k"); err != nil || !ok || v != 1 {
		t.Fatalf("second Get = %d %v %v", v, ok, err)
	}
}

func TestNotFoundIsRememberedOnlyWithMissTTL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missTTL time.Duration
		loads   int32
	}{{"no memory of misses", 0, 3}, {"misses remembered", time.Minute, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := New(Options{MissTTL: tc.missTTL}, func(context.Context, string, string) (int, error) {
				calls.Add(1)
				return 0, fmt.Errorf("meter x: %w", ErrNotFound)
			})
			for range 3 {
				if _, ok, err := c.Get(context.Background(), "s", "k"); ok || err != nil {
					t.Fatalf("Get = %v %v, want not found without an error", ok, err)
				}
			}
			if calls.Load() != tc.loads {
				t.Fatalf("loads = %d, want %d", calls.Load(), tc.loads)
			}
		})
	}
}

func TestEntriesExpireAndTheCacheIsBounded(t *testing.T) {
	var calls atomic.Int32
	c := New(Options{Size: 2, HitTTL: 60 * time.Millisecond}, func(context.Context, string, string) (int, error) {
		return int(calls.Add(1)), nil
	})
	ctx := context.Background()
	c.Get(ctx, "s", "a")
	c.Get(ctx, "s", "b")
	c.Get(ctx, "s", "c") // evicts a
	if calls.Load() != 3 {
		t.Fatalf("loads = %d", calls.Load())
	}
	c.Get(ctx, "s", "c")
	if calls.Load() != 3 {
		t.Fatal("c should be cached")
	}
	c.Get(ctx, "s", "a")
	if calls.Load() != 4 {
		t.Fatal("a should have been evicted by the size bound")
	}
	time.Sleep(120 * time.Millisecond)
	c.Get(ctx, "s", "a")
	if calls.Load() != 5 {
		t.Fatal("a should have expired")
	}
}

func TestLoadTimeoutBoundsTheLoad(t *testing.T) {
	c := New(Options{LoadTimeout: 30 * time.Millisecond}, func(ctx context.Context, _, _ string) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	if _, _, err := c.Get(context.Background(), "s", "k"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}
