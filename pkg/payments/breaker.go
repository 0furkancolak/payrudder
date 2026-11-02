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

// SharedBreaker is the part of an account's circuit that the replicas of a worker keep outside their processes (Breaker.Shared), so that an outage one replica found is not
// found again by each of the others, charge by charge: while one has the circuit open, all refuse. It is one account's: the implementation is built for it. It never fails:
// when the shared store cannot be reached it answers as if no other replica existed, and the breaker goes on with what this replica knows.
type SharedBreaker interface {
	// OpenFor is how long the circuit stays open for every replica, or 0 when none has it open.
	OpenFor() time.Duration
	// Opened records that a replica opened the circuit for window (it also frees the probe).
	Opened(window time.Duration)
	// Closed records that the provider answered: the circuit is closed for every replica (it also frees the probe).
	Closed()
	// Probe takes the lease on the one probe charge of the half-open state for lease, and says false when another replica holds it.
	Probe(lease time.Duration) bool
}

// Breaker is the circuit breaker of one provider account. It counts failures in a row (IsOutage); a success, or any answer from the provider that is not an outage
// (a decline is the provider working), resets the count and closes the circuit. It is safe for concurrent use.
//
// shortcut: with a SharedBreaker the open window and the probe lease are shared, the count of failures is not: a replica that was closed when the window ended lets charges
// through without a probe until its own count, or a probing replica's failure, opens the circuit again. Share the count too if that leak matters.
type Breaker struct {
	// Shared, when set before the breaker is used, makes the circuit one for all replicas (nil: this process only).
	Shared SharedBreaker

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
		if wait := b.sharedOpenFor(); wait > 0 { // another replica has it open
			return nil, &TransientError{Err: ErrCircuitOpen, RetryAfter: wait}
		}
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
	// this caller is the probe, unless another replica has opened the circuit again since or holds the lease (asked with the lock held: it happens once a cool-down, and
	// the calls are bounded)
	if b.Shared != nil {
		if wait := b.Shared.OpenFor(); wait > 0 {
			b.mu.Unlock()
			return nil, &TransientError{Err: ErrCircuitOpen, RetryAfter: wait}
		}
		if !b.Shared.Probe(b.s.OpenFor) {
			b.mu.Unlock()
			return nil, &TransientError{Err: ErrCircuitOpen, RetryAfter: time.Second}
		}
	}
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

// Observe records an outcome that did not come from a charge (a health check): an outage counts as a failure, anything else as the provider answering. An answer also
// closes the shared circuit, which another replica may have opened.
func (b *Breaker) Observe(err error) {
	b.record(err, false)
	if b.Shared != nil && (err == nil || !IsOutage(err)) {
		b.Shared.Closed()
	}
}

// sharedOpenFor is how long another replica keeps the circuit open, or 0.
func (b *Breaker) sharedOpenFor() time.Duration {
	if b.Shared == nil {
		return 0
	}
	return b.Shared.OpenFor()
}

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
		if b.Shared != nil {
			if to == BreakerOpen {
				b.Shared.Opened(b.s.OpenFor)
			} else {
				b.Shared.Closed()
			}
		}
	}
}

func (b *Breaker) notify(to BreakerState, failures int) {
	if b.onChange != nil {
		b.onChange(to, failures)
	}
}
