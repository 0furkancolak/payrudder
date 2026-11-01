package payments

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var outage = &TransientError{Err: errors.New("503")}

// Closed until Failures outages in a row, open for OpenFor (fast, with the time left), then one probe: a failed probe opens it again, a good one closes it. A decline
// is the provider working and resets the count.
func TestBreakerOpensProbesAndCloses(t *testing.T) {
	c := &clock{t: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	var seen []BreakerState
	b := NewBreaker(BreakerSettings{Failures: 3, OpenFor: time.Minute}, c.now, func(s BreakerState, _ int) { seen = append(seen, s) })

	call := func(err error) error {
		done, aerr := b.Allow()
		if aerr != nil {
			return aerr
		}
		done(err)
		return nil
	}
	for i := 0; i < 2; i++ {
		_ = call(outage)
	}
	_ = call(&DeclinedError{Code: "card_declined"}) // the provider answered: the count starts again
	_ = call(outage)
	_ = call(outage)
	if b.State() != BreakerClosed {
		t.Fatalf("state = %s after 2 outages since the decline", b.State())
	}
	_ = call(ErrTimeout) // a timeout is an outage too
	if b.State() != BreakerOpen {
		t.Fatalf("state = %s, want open after 3 in a row", b.State())
	}

	err := call(nil)
	var tr *TransientError
	if !errors.As(err, &tr) || !errors.Is(err, ErrCircuitOpen) || tr.RetryAfter <= 0 || tr.RetryAfter > time.Minute {
		t.Fatalf("open: %v, want a transient circuit-open error with the time left", err)
	}

	c.add(time.Minute)
	if b.State() != BreakerHalfOpen {
		t.Fatalf("after the cool-down: %s", b.State())
	}
	done, err := b.Allow() // the probe
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("a second call while the probe is out: %v, want refused", err)
	}
	done(outage) // the probe failed
	if b.State() != BreakerOpen {
		t.Fatalf("after a failed probe: %s", b.State())
	}
	if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("a failed probe starts another cool-down: %v", err)
	}

	c.add(time.Minute)
	done, err = b.Allow()
	if err != nil {
		t.Fatal(err)
	}
	done(nil)
	done(outage) // an outcome is recorded once
	if b.State() != BreakerClosed {
		t.Fatalf("after a good probe: %s", b.State())
	}
	want := []BreakerState{BreakerOpen, BreakerHalfOpen, BreakerOpen, BreakerHalfOpen, BreakerClosed}
	if len(seen) != len(want) {
		t.Fatalf("changes = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("changes = %v, want %v", seen, want)
			break
		}
	}
}

// A health check feeds the same breaker: outages open it, and a check that finds the provider answering closes it before any charge has to probe.
func TestBreakerObservesHealthChecks(t *testing.T) {
	c := &clock{t: time.Now()}
	b := NewBreaker(BreakerSettings{Failures: 2, OpenFor: time.Hour}, c.now, nil)
	b.Observe(ErrUnreachable)
	b.Observe(ErrUnreachable)
	if b.State() != BreakerOpen {
		t.Fatalf("state = %s", b.State())
	}
	b.Observe(ErrBadCredentials) // the provider answered, refusing the credentials: reachable, so not an outage
	if b.State() != BreakerClosed {
		t.Fatalf("state = %s, want closed once the provider answers", b.State())
	}
}

// Only proof that the request never left is "not processed"; a timeout, a 5xx and a plain transient error are not.
func TestNotProcessedIsOnlyWhatIsProven(t *testing.T) {
	dial := &TransientError{Err: &url.Error{Op: "Post", URL: "https://p", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}}
	read := &TransientError{Err: &url.Error{Op: "Post", URL: "https://p", Err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}}}
	dns := &TransientError{Err: &url.Error{Op: "Post", URL: "https://p", Err: &net.DNSError{Err: "no such host", Name: "p"}}}
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"a connection refused":             {dial, true},
		"a host that does not resolve":     {dns, true},
		"a reset in the middle":            {read, false},
		"a timeout":                        {ErrTimeout, false},
		"a deadline":                       {context.DeadlineExceeded, false},
		"a 503 with no word on processing": {&TransientError{Err: errors.New("psp answered 503")}, false},
		"a decline":                        {&DeclinedError{Code: "x"}, false},
		"an open circuit (not called)":     {&TransientError{Err: ErrCircuitOpen}, true},
		"an adapter that says so":          {&NotProcessedError{Err: &TransientError{Err: errors.New("503 before processing")}}, true},
		"that, wrapped":                    {errors.Join(errors.New("charge"), &NotProcessedError{Err: outage}), true},
		"nothing":                          {nil, false},
	} {
		if got := NotProcessed(c.err); got != c.want {
			t.Errorf("%s: NotProcessed = %v, want %v", name, got, c.want)
		}
	}
	if !IsOutage(dial) || !IsOutage(ErrTimeout) || !IsOutage(ErrUnreachable) || IsOutage(&DeclinedError{}) || IsOutage(ErrBadCredentials) || IsOutage(nil) {
		t.Error("IsOutage")
	}
}

// The failover key is new, depends on the account, is the same on a second delivery, and is found by its prefix.
func TestFailoverKey(t *testing.T) {
	k := "inv:charge:0"
	a, b := FailoverKey(k, "stripe~eu"), FailoverKey(k, "stripe~us")
	if a == k || a == b || a != FailoverKey(k, "stripe~eu") {
		t.Errorf("keys %q %q", a, b)
	}
	if !IsFailoverKey(a) || IsFailoverKey(k) || a[:len(FailoverPrefix(k))] != FailoverPrefix(k) {
		t.Errorf("prefix of %q", a)
	}
}

// A probe that never reports back (its caller was lost) does not hold the circuit shut for ever: after another cool-down the next caller is the probe.
func TestBreakerLetsAnotherProbeInAfterALostOne(t *testing.T) {
	c := &clock{t: time.Now()}
	b := NewBreaker(BreakerSettings{Failures: 1, OpenFor: time.Minute}, c.now, nil)
	b.Observe(outage)
	c.add(time.Minute)
	if _, err := b.Allow(); err != nil { // the probe, whose done is never called
		t.Fatal(err)
	}
	c.add(30 * time.Second)
	if _, err := b.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("while the probe may still be out: %v", err)
	}
	c.add(31 * time.Second)
	if done, err := b.Allow(); err != nil {
		t.Fatalf("after the lease: %v", err)
	} else {
		done(nil)
	}
	if b.State() != BreakerClosed {
		t.Errorf("state = %s", b.State())
	}
}
