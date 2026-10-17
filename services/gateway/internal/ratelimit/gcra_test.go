package ratelimit

import (
	"testing"
	"time"
)

// TestBurstThenRate drives the GCRA with a fake clock: the burst is admitted at once, then exactly the sustained rate.
func TestBurstThenRate(t *testing.T) {
	lim := Limit{PerSecond: 100, Burst: 500}
	now := time.Unix(1_790_000_000, 0)
	var tat time.Time

	allowed := 0
	for range 600 {
		d := step(tat, now, lim, 1)
		if d.Allowed {
			allowed++
			tat = d.TAT
		}
	}
	if allowed != 500 {
		t.Fatalf("burst admitted %d, want 500", allowed)
	}

	d := step(tat, now, lim, 1)
	if d.Allowed || d.RetryAfter != 10*time.Millisecond {
		t.Fatalf("after burst: %+v, want rejected with retry 10ms", d)
	}

	// Over the next second, exactly rate units are admitted.
	allowed = 0
	for i := 1; i <= 1000; i++ {
		d := step(tat, now.Add(time.Duration(i)*time.Millisecond), lim, 1)
		if d.Allowed {
			allowed++
			tat = d.TAT
		}
	}
	if allowed != 100 {
		t.Fatalf("sustained: admitted %d in 1s, want 100", allowed)
	}
}

func TestCostIsChargedPerUnit(t *testing.T) {
	lim := Limit{PerSecond: 100, Burst: 500}
	now := time.Unix(0, 0)
	d := step(time.Time{}, now, lim, 500)
	if !d.Allowed {
		t.Fatal("a batch equal to the burst was rejected")
	}
	if d2 := step(d.TAT, now, lim, 1); d2.Allowed {
		t.Fatal("batching bypassed the limit")
	}
	if d3 := step(time.Time{}, now, lim, 501); d3.Allowed || d3.RetryAfter != 10*time.Millisecond {
		t.Fatalf("batch above burst: %+v", d3)
	}
}

func TestIdleRefills(t *testing.T) {
	lim := Limit{PerSecond: 10, Burst: 5}
	now := time.Unix(0, 0)
	d := step(time.Time{}, now, lim, 5)
	if !d.Allowed {
		t.Fatal("initial burst rejected")
	}
	if step(d.TAT, now.Add(10*time.Second), lim, 5).Allowed != true {
		t.Fatal("burst not refilled after idling")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "1", 10 * time.Millisecond: "1", time.Second: "1", 1500 * time.Millisecond: "2"} {
		if got := RetryAfterSeconds(d); got != want {
			t.Errorf("RetryAfterSeconds(%v) = %s, want %s", d, got, want)
		}
	}
}
