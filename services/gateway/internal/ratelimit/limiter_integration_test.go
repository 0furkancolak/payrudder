//go:build integration

package ratelimit

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/0furkancolak/quotify/pkg/testkit"
)

func TestRedisScriptMatchesGoImplementation(t *testing.T) {
	ctx := context.Background()
	opts, err := redis.ParseURL(testkit.StartRedis(t))
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	lim := Limit{PerSecond: 100, Burst: 500}
	start := time.Unix(1_790_000_000, 0)
	var tat time.Time
	key := "gw:rl:test:ingest"
	// Same sequence of (time, cost) through the Lua script with a fixed clock and through step().
	for i := 0; i < 2000; i++ {
		now := start.Add(time.Duration(i) * 3 * time.Millisecond)
		cost := 1 + i%4
		want := step(tat, now, lim, cost)
		if want.Allowed {
			tat = want.TAT
		}
		res, err := gcraScript.Run(ctx, rdb, []string{key}, lim.emission().Microseconds(), lim.Burst, cost, now.UnixMicro()).Int64Slice()
		if err != nil {
			t.Fatal(err)
		}
		if (res[0] == 1) != want.Allowed || time.Duration(res[1])*time.Microsecond != want.RetryAfter {
			t.Fatalf("step %d: redis %v, go %+v", i, res, want)
		}
	}
}

// TestFreeTierIngest charges 600 events in one go against a free-tier key (burst 500): the excess is refused.
func TestFreeTierIngest(t *testing.T) {
	ctx := context.Background()
	opts, err := redis.ParseURL(testkit.StartRedis(t))
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()
	l, err := NewLimiter(rdb, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	allowed := 0
	var last Decision
	start := time.Now()
	for range 600 {
		d, _ := l.Allow(ctx, "tenant-free", "free", ClassIngest, 1)
		if d.Allowed {
			allowed++
		} else {
			last = d
		}
	}
	// Units refill at 100/s while the loop runs: never fewer than the burst, never more than burst + elapsed × rate.
	maxAllowed := 500 + int(time.Since(start).Seconds()*100) + 1
	if allowed < 500 || allowed > maxAllowed {
		t.Fatalf("admitted %d of 600, want between 500 and %d", allowed, maxAllowed)
	}
	if last.RetryAfter <= 0 || RetryAfterSeconds(last.RetryAfter) != "1" {
		t.Fatalf("retry after = %v", last.RetryAfter)
	}

	// Redis gone: fail open to the local bucket instead of erroring.
	_ = rdb.Close()
	d, err := l.Allow(ctx, "tenant-free", "free", ClassWrite, 1)
	if err != nil || !d.Allowed {
		t.Fatalf("fallback decision = %+v, %v", d, err)
	}
}
