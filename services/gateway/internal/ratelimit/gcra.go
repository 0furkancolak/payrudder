// Package ratelimit implements per-tenant GCRA (generic cell rate algorithm) rate limiting.
//
// State is one "theoretical arrival time" (TAT) per key. The same algorithm runs in Redis (a Lua script, shared by all
// gateway replicas) and in process (the fail-open fallback when Redis is unavailable).
package ratelimit

import "time"

// Limit is a sustained rate with a burst allowance.
type Limit struct {
	PerSecond float64
	Burst     int
}

// emission is the time one unit "costs": 1 / rate, truncated to whole microseconds so the Go and Lua
// implementations make identical decisions.
func (l Limit) emission() time.Duration {
	return time.Duration(float64(time.Second) / l.PerSecond).Truncate(time.Microsecond)
}

// Decision is the outcome of one GCRA step.
type Decision struct {
	Allowed bool
	// RetryAfter is how long until the same cost would be allowed (0 when allowed).
	RetryAfter time.Duration
	// TAT is the new theoretical arrival time to store (unchanged when rejected).
	TAT time.Time
}

// step applies GCRA for cost units at now, given the stored TAT (zero if none).
// A request is allowed if, after adding cost × emission to the TAT, the TAT is at most burst × emission ahead of now.
func step(tat, now time.Time, l Limit, cost int) Decision {
	if tat.Before(now) {
		tat = now
	}
	em := l.emission()
	newTAT := tat.Add(time.Duration(cost) * em)
	allowAt := newTAT.Add(-time.Duration(l.Burst) * em)
	if allowAt.After(now) {
		return Decision{Allowed: false, RetryAfter: allowAt.Sub(now), TAT: tat}
	}
	return Decision{Allowed: true, TAT: newTAT}
}
