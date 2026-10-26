package period

import (
	"testing"
	"time"
)

func TestWindowOf(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	ny := mustLoc(t, "America/New_York")
	cases := []struct {
		name       string
		w          Window
		at         time.Time
		loc        *time.Location
		id         string
		start, end time.Time
	}{
		// 21:30Z on Thursday 5 Nov is 00:30 on Friday 6 Nov in Istanbul (UTC+3).
		{"hour istanbul", Hour, time.Date(2026, 11, 5, 21, 30, 0, 0, time.UTC), ist, "2026-11-06T00",
			time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC), time.Date(2026, 11, 5, 22, 0, 0, 0, time.UTC)},
		{"day istanbul", Day, time.Date(2026, 11, 5, 21, 30, 0, 0, time.UTC), ist, "2026-11-06",
			time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC), time.Date(2026, 11, 6, 21, 0, 0, 0, time.UTC)},
		// Friday 6 Nov 2026 is in ISO week 45, which starts on Monday 2 Nov.
		{"week istanbul", Week, time.Date(2026, 11, 5, 21, 30, 0, 0, time.UTC), ist, "2026-W45",
			time.Date(2026, 11, 1, 21, 0, 0, 0, time.UTC), time.Date(2026, 11, 8, 21, 0, 0, 0, time.UTC)},
		{"month istanbul", Month, time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC), ist, "2026-11",
			time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC), time.Date(2026, 11, 30, 21, 0, 0, 0, time.UTC)},
		// 1 Jan 2027 is a Friday of ISO week 53 of 2026.
		{"week across the year", Week, time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC), time.UTC, "2026-W53",
			time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 4, 0, 0, 0, 0, time.UTC)},
		// DST ends in New York on 1 Nov 2026 at 02:00 EDT: the day is 25 hours long and 01:00 happens twice.
		{"day of the dst change", Day, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), ny, "2026-11-01",
			time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC)},
		{"repeated hour", Hour, time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), ny, "2026-11-01T01",
			time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, start, end := WindowOf(c.w, c.at, c.loc)
			if id != c.id || !start.Equal(c.start) || !end.Equal(c.end) {
				t.Fatalf("WindowOf = %s [%s, %s), want %s [%s, %s)", id, start.UTC(), end.UTC(), c.id, c.start, c.end)
			}
			w, bs, be, err := WindowBounds(id, c.loc)
			if err != nil || w != c.w || !bs.Equal(start) || !be.Equal(end) {
				t.Fatalf("WindowBounds(%s) = %s [%s, %s) %v, want %s [%s, %s)", id, w, bs.UTC(), be.UTC(), err, c.w, start.UTC(), end.UTC())
			}
			if !start.After(c.at) && end.After(c.at) {
				return
			}
			t.Fatalf("%s is not inside [%s, %s)", c.at, start, end)
		})
	}
}

func TestWindowBoundsRejects(t *testing.T) {
	for _, bad := range []string{"", "2026", "2026-W00", "2026-W54", "2025-W53", "2026-11-31", "2026-02-29", "2026-11-05T24", "2026-11-05 13", "2026-11-0a", "2026-W+1", "2026-13-01"} {
		if _, _, _, err := WindowBounds(bad, time.UTC); err == nil {
			t.Errorf("WindowBounds(%q) accepted", bad)
		}
	}
}

func TestParseWindow(t *testing.T) {
	for in, want := range map[string]Window{"": Month, "month": Month, "hour": Hour, "day": Day, "week": Week} {
		if got, ok := ParseWindow(in); !ok || got != want {
			t.Errorf("ParseWindow(%q) = %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"year", "lifetime", "Month"} {
		if _, ok := ParseWindow(bad); ok {
			t.Errorf("ParseWindow(%q) accepted", bad)
		}
	}
}
