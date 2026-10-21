package command

import (
	"testing"
	"time"
)

func TestNextAttemptFollowsTheBackoffThenGivesUp(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	want := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour}
	for i, d := range want {
		at, ok := NextAttempt(now, i+1)
		if !ok || !at.Equal(now.Add(d)) {
			t.Errorf("after attempt %d: %v %v, want +%s", i+1, at, ok, d)
		}
	}
	if _, ok := NextAttempt(now, len(want)+1); ok {
		t.Error("the sixth attempt is the last one")
	}
	if _, ok := NextAttempt(now, 0); ok {
		t.Error("no attempt yet")
	}
}
