package ratelimit

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// With Redis down every replica limits on its own, with its share of the burst (burst ÷ replicas). A batch larger than the share used to be refused for ever
// (GCRA never admits a cost above the burst) while the client was told to retry in a second: three replicas, a free tenant's 500-event burst gives 166, and a
// 200-event batch could not get in however long it waited. The batch now costs the whole share, so it is admitted when the share is full.
func TestFallbackAdmitsABatchLargerThanItsShare(t *testing.T) {
	l, err := NewLimiter(nil, 3, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_790_000_000, 0)
	l.now = func() time.Time { return now }
	ctx := context.Background()

	d, err := l.Allow(ctx, "tenant", "free", ClassIngest, 200)
	if err != nil || !d.Allowed {
		t.Fatalf("a 200-event batch with a share of 166 was refused: %+v %v", d, err)
	}
	// The share is spent: the next batch waits for it to refill, then gets in.
	d, _ = l.Allow(ctx, "tenant", "free", ClassIngest, 200)
	if d.Allowed || d.RetryAfter <= 0 {
		t.Fatalf("a second batch at once: %+v, want refused with a wait", d)
	}
	now = now.Add(d.RetryAfter)
	if d, _ = l.Allow(ctx, "tenant", "free", ClassIngest, 200); !d.Allowed {
		t.Fatalf("after the advised wait the batch is still refused: %+v", d)
	}
}
