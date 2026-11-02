package payments

import (
	"sync"
	"time"
)

// BreakerState is where a circuit breaker is.
type BreakerState string

const (
	// BreakerClosed: charges go to the provider.
	BreakerClosed BreakerState = "closed"
	// BreakerOpen: the provider has failed too often in a row; charges fail fast, without a call, until the cool-down has passed.
	BreakerOpen BreakerState = "open"
	// BreakerHalfOpen: the cool-down has passed; one charge at a time is let through to find out whether the provider is back.
	BreakerHalfOpen BreakerState = "half_open"
)

// BreakerSettings are the breaker's numbers. All are defaults, not agreed (docs/provider-health.md).
type BreakerSettings struct {
	// Failures is how many outage-class failures in a row open the circuit (default 5).
	Failures int
	// OpenFor is how long it stays open before one probe is let through (default 30 s).
	OpenFor time.Duration
}

// DefaultBreaker are the defaults, not agreed.
var DefaultBreaker = BreakerSettings{Failures: 5, OpenFor: 30 * time.Second}

// Breaker is the circuit breaker of one provider account. It counts failures in a row (IsOutage); a success, or any answer from the provider that is not an outage
// (a decline is the provider working), resets the count and closes the circuit. It is safe for concurrent use.
type Breaker struct {
	mu       sync.Mutex
	s        BreakerSettings
	now      func() time.Time
	state    BreakerState
	failures int
	openedAt time.Time
	probing  bool
	probeAt  time.Time
	onChange func(BreakerState, int)
}

// NewBreaker returns a closed breaker. A zero field of s takes the default. now nil is time.Now; onChange, when set, is told of every change of state (with the failures
// in a row), after the breaker's lock is released.
func NewBreaker(s BreakerSettings, now func() time.Time, onChange func(BreakerState, int)) *Breaker {
	if s.Failures <= 0 {
		s.Failures = DefaultBreaker.Failures
	}
	if s.OpenFor <= 0 {
		s.OpenFor = DefaultBreaker.OpenFor
	}
	if now == nil {
		now = time.Now
	}
	return &Breaker{s: s, now: now, state: BreakerClosed, onChange: onChange}
}

// State is the state now (an open circuit whose cool-down has passed is reported half-open).
func (b *Breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == BreakerOpen && b.now().Sub(b.openedAt) >= b.s.OpenFor {
		return BreakerHalfOpen
	}
	return b.state
}

// Allow asks whether a charge may go to the provider. When it may, done must be called with the outcome of the call (nil, or the error). When the circuit is open it
// returns a *TransientError that wraps ErrCircuitOpen, with RetryAfter set to what is left of the cool-down, and no done. In the half-open state exactly one caller is
// let through at a time; the others are refused until that one's done.
func (b *Breaker) Allow() (done func(err error), err error) {
	b.mu.Lock()
	switch {
	case b.state == BreakerClosed:
		b.mu.Unlock()
		return b.finish(false), nil
	case b.state == BreakerOpen && b.now().Sub(b.openedAt) < b.s.OpenFor:
		wait := b.s.OpenFor - b.now().Sub(b.openedAt)
		b.mu.Unlock()
		return nil, &TransientError{Err: ErrCircuitOpen, RetryAfter: wait}
	case b.probing && b.now().Sub(b.probeAt) < b.s.OpenFor:
		b.mu.Unlock()
		return nil, &TransientError{Err: ErrCircuitOpen, RetryAfter: time.Second}
	}
	// open and cooled down, half-open with no probe out, or a probe that never reported back (a lease of OpenFor, so a lost probe cannot hold the circuit shut):
	// this caller is the probe
	b.state, b.probing, b.probeAt = BreakerHalfOpen, true, b.now()
	b.mu.Unlock()
	b.notify(BreakerHalfOpen, b.failures)
	return b.finish(true), nil
}

// finish returns the function that records an outcome, once.
func (b *Breaker) finish(probe bool) func(error) {
	var once sync.Once
	return func(err error) { once.Do(func() { b.record(err, probe) }) }
}

// Observe records an outcome that did not come from a charge (a health check): an outage counts as a failure, anything else as the provider answering.
func (b *Breaker) Observe(err error) { b.record(err, false) }

func (b *Breaker) record(err error, probe bool) {
	b.mu.Lock()
	if probe {
		b.probing = false
	}
	var to BreakerState
	switch {
	case err == nil || !IsOutage(err):
		b.failures = 0
		if b.state != BreakerClosed {
			b.state, to = BreakerClosed, BreakerClosed
		}
	case b.state == BreakerHalfOpen:
		b.failures++
		b.state, b.openedAt, to = BreakerOpen, b.now(), BreakerOpen // the probe failed: another cool-down
	default:
		b.failures++
		if b.state == BreakerClosed && b.failures >= b.s.Failures {
			b.state, b.openedAt, to = BreakerOpen, b.now(), BreakerOpen
		}
	}
	failures := b.failures
	b.mu.Unlock()
	if to != "" {
		b.notify(to, failures)
	}
}

func (b *Breaker) notify(to BreakerState, failures int) {
	if b.onChange != nil {
		b.onChange(to, failures)
	}
}
