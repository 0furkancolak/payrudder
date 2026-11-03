package ratelimit

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// countingScripter is a Redis that fails every script and counts the calls (an error is the only reply a test needs: it sends AllowKey down the fallback).
type countingScripter struct{ calls int }

func (c *countingScripter) Eval(ctx context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	c.calls++
	return redis.NewCmd(ctx, "eval")
}
func (c *countingScripter) EvalSha(ctx context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	c.calls++
	return redis.NewCmd(ctx, "evalsha")
}
func (c *countingScripter) EvalRO(ctx context.Context, s string, k []string, a ...interface{}) *redis.Cmd {
	return c.Eval(ctx, s, k, a...)
}
func (c *countingScripter) EvalShaRO(ctx context.Context, s string, k []string, a ...interface{}) *redis.Cmd {
	return c.EvalSha(ctx, s, k, a...)
}
func (c *countingScripter) ScriptExists(ctx context.Context, _ ...string) *redis.BoolSliceCmd {
	return redis.NewBoolSliceCmd(ctx)
}
func (c *countingScripter) ScriptLoad(ctx context.Context, _ string) *redis.StringCmd {
	return redis.NewStringCmd(ctx)
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A key without a percentage costs nothing extra: no Redis call and no local bucket.
func TestAllowKeyWithoutAPercentageCostsNothing(t *testing.T) {
	rdb := &countingScripter{}
	l, err := NewLimiter(rdb, 1, quiet())
	if err != nil {
		t.Fatal(err)
	}
	for _, pct := range []int{0, -1, 101} {
		if d, err := l.AllowKey(context.Background(), "k1", "t1", "free", ClassWrite, 1, pct); err != nil || !d.Allowed {
			t.Fatalf("percent %d: %+v %v", pct, d, err)
		}
	}
	if d, _ := l.AllowKey(context.Background(), "", "t1", "free", ClassWrite, 1, 50); !d.Allowed {
		t.Fatal("an empty key id was limited")
	}
	if rdb.calls != 0 || len(l.local) != 0 {
		t.Fatalf("redis calls %d, local buckets %d; want none", rdb.calls, len(l.local))
	}
}

// With Redis down the key's own bucket still limits it: it is refused after its share while the tenant's bucket (another key, no percentage) still has room,
// and the key's cost never touches the tenant's bucket.
func TestAllowKeyRefusesWhileTheTenantHasRoom(t *testing.T) {
	rdb := &countingScripter{}
	l, err := NewLimiter(rdb, 1, quiet())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_790_000_000, 0)
	l.now = func() time.Time { return now }
	ctx := context.Background()

	// free write is 5/s, burst 10; 20% is 1/s, burst 2.
	for i := range 2 {
		if d, _ := l.AllowKey(ctx, "k1", "t1", "free", ClassWrite, 1, 20); !d.Allowed {
			t.Fatalf("call %d inside the key's burst was refused", i)
		}
	}
	d, _ := l.AllowKey(ctx, "k1", "t1", "free", ClassWrite, 1, 20)
	if d.Allowed || d.RetryAfter <= 0 || d.RetryAfter > time.Second {
		t.Fatalf("third call: %+v, want refused with a wait of at most a second", d)
	}
	// Another key of the tenant has its own bucket, and the tenant's bucket is untouched.
	if d, _ := l.AllowKey(ctx, "k2", "t1", "free", ClassWrite, 1, 20); !d.Allowed {
		t.Fatal("a second key shares the first key's bucket")
	}
	if d, _ := l.Allow(ctx, "t1", "free", ClassWrite, 10); !d.Allowed {
		t.Fatalf("the tenant bucket was drained by the key's calls: %+v", d)
	}
	// After the wait the key is let in again.
	now = now.Add(d.RetryAfter + time.Second)
	if d, _ := l.AllowKey(ctx, "k1", "t1", "free", ClassWrite, 1, 20); !d.Allowed {
		t.Fatalf("after the wait: %+v", d)
	}
}

// The burst is at least 1 however small the share, and a batch above the burst costs the whole burst instead of being refused for ever.
func TestAllowKeyBurstFloorAndLargeBatch(t *testing.T) {
	l, err := NewLimiter(nil, 1, quiet())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_790_000_000, 0)
	l.now = func() time.Time { return now }
	ctx := context.Background()

	// free analytics: 0.5/s, burst 5; 1% is a burst of 0 rounded up to 1.
	if d, _ := l.AllowKey(ctx, "k1", "t1", "free", ClassAnalytics, 1, 1); !d.Allowed {
		t.Fatal("a burst of 1 refused its first call")
	}
	if d, _ := l.AllowKey(ctx, "k1", "t1", "free", ClassAnalytics, 1, 1); d.Allowed {
		t.Fatal("a burst of 1 admitted a second call at once")
	}
	// free ingest 1%: burst 5; a batch of 500 takes the whole burst and is admitted when it is full.
	if d, _ := l.AllowKey(ctx, "k2", "t1", "free", ClassIngest, 500, 1); !d.Allowed {
		t.Fatal("a batch above the key's burst was refused for ever")
	}
}
