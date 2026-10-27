// Package backoff paces loops that retry against a dependency that is down, without flooding the log.
package backoff

import (
	"context"
	"log/slog"
	"time"
)

// Retry paces a loop that keeps failing against a dependency that is down: each failure waits twice as long as the last (up to Max) and the log says
// so once, then at most every Every with the number of failures it held back, and once more when the dependency is back. Not safe for concurrent use:
// every loop owns one.
type Retry struct {
	Base, Max time.Duration // default 1s, 30s
	Every     time.Duration // default 30s

	failures   int
	suppressed int
	lastLog    time.Time
}

// Fail records a failure, logs it (throttled) and sleeps the backoff. It returns early when ctx is done.
func (r *Retry) Fail(ctx context.Context, log *slog.Logger, msg string, err error, args ...any) {
	r.failures++
	now := time.Now()
	every := r.Every
	if every <= 0 {
		every = 30 * time.Second
	}
	if r.failures == 1 || now.Sub(r.lastLog) >= every {
		a := append([]any{"error", err, "failures", r.failures, "held_back", r.suppressed}, args...)
		log.WarnContext(ctx, msg, a...)
		r.lastLog, r.suppressed = now, 0
	} else {
		r.suppressed++
	}
	sleep(ctx, r.delay())
}

// OK ends a streak of failures; when there was one, it logs that the dependency is back.
func (r *Retry) OK(ctx context.Context, log *slog.Logger, msg string, args ...any) {
	if r.failures == 0 {
		return
	}
	log.InfoContext(ctx, msg, append([]any{"failures", r.failures}, args...)...)
	r.failures, r.suppressed = 0, 0
}

func (r *Retry) delay() time.Duration {
	base, limit := r.Base, r.Max
	if base <= 0 {
		base = time.Second
	}
	if limit <= 0 {
		limit = 30 * time.Second
	}
	d := base
	for i := 1; i < r.failures && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
