package delivery

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLimiterIsPerTenantAndRefills(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	l := &Limiter{PerMinute: 60, Burst: 3, Now: func() time.Time { return now }}
	a, b := uuid.New(), uuid.New()
	for i := 0; i < 3; i++ {
		if !l.Allow(a) {
			t.Fatalf("mail %d of the burst was refused", i+1)
		}
	}
	if l.Allow(a) {
		t.Error("a fourth mail at once passed a burst of 3")
	}
	if !l.Allow(b) {
		t.Error("one tenant's flood stopped another tenant")
	}
	now = now.Add(2 * time.Second) // 60 a minute is one a second
	first, second, third := l.Allow(a), l.Allow(a), l.Allow(a)
	if !first || !second || third {
		t.Error("two seconds should refill exactly two tokens")
	}
	now = now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		l.Allow(a)
	}
	if l.Allow(a) {
		t.Error("the bucket must not hold more than the burst")
	}
}

func TestLimiterOffWhenZero(t *testing.T) {
	var none *Limiter
	off := &Limiter{PerMinute: 0, Burst: 1}
	id := uuid.New()
	for i := 0; i < 100; i++ {
		if !none.Allow(id) || !off.Allow(id) {
			t.Fatal("a limit of 0 (or none) limits nothing")
		}
	}
}
