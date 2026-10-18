package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestCrossed(t *testing.T) {
	tests := []struct {
		name               string
		before, after, lim int64
		pct                int32
		want               bool
	}{
		{"below", 10, 79, 100, 80, false},
		{"reaches exactly", 79, 80, 100, 80, true},
		{"jumps over", 10, 95, 100, 80, true},
		{"already past", 80, 90, 100, 80, false},
		{"100 percent", 99, 100, 100, 100, true},
		{"fractional limit 10.500 scale 3", 10499, 10500, 10500, 100, true},
		{"fractional limit one before", 10498, 10499, 10500, 100, false},
		{"80 percent of 10.500", 8399, 8400, 10500, 80, true},
		{"unlimited", 0, 1 << 40, 0, 100, false},
		{"no overflow near 2^53", MaxCounter - 2, MaxCounter - 1, MaxCounter - 1, 100, true},
	}
	for _, tc := range tests {
		if got := Crossed(tc.before, tc.after, tc.lim, tc.pct); got != tc.want {
			t.Errorf("%s: Crossed(%d,%d,%d,%d) = %v, want %v", tc.name, tc.before, tc.after, tc.lim, tc.pct, got, tc.want)
		}
	}
}

func TestLimitEventIDIsDeterministic(t *testing.T) {
	a, c := uuid.New(), uuid.New()
	first, second := LimitEventID(a, c, "api", "2026-11", 80), LimitEventID(a, c, "api", "2026-11", 80)
	if first != second {
		t.Fatal("same inputs gave different ids")
	}
	if LimitEventID(a, c, "api", "2026-11", 80) == LimitEventID(a, c, "api", "2026-11", 100) {
		t.Fatal("different thresholds share an id")
	}
}
