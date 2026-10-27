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
		// A year is a wall-clock year of the tenant: 21:00Z on 31 Dec is already 1 Jan in Istanbul, and the year of a leap day is 366 days.
		{"year istanbul new year", Year, time.Date(2026, 12, 31, 21, 0, 0, 0, time.UTC), ist, "2027",
			time.Date(2026, 12, 31, 21, 0, 0, 0, time.UTC), time.Date(2027, 12, 31, 21, 0, 0, 0, time.UTC)},
		{"year istanbul before new year", Year, time.Date(2026, 12, 31, 20, 59, 59, 0, time.UTC), ist, "2026",
			time.Date(2025, 12, 31, 21, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 21, 0, 0, 0, time.UTC)},
		{"leap year in new york", Year, time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC), ny, "2028",
			time.Date(2028, 1, 1, 5, 0, 0, 0, time.UTC), time.Date(2029, 1, 1, 5, 0, 0, 0, time.UTC)},
		// The year spans both DST changes: its ends are EST, its middle EDT.
		{"year with dst", Year, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), ny, "2026",
			time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 5, 0, 0, 0, time.UTC)},
		// Spring forward in New York on 8 Mar 2026: 02:00 does not exist, the day is 23 hours long.
		{"day of the spring dst change", Day, time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), ny, "2026-03-08",
			time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC), time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)},
		{"hour after the skipped hour", Hour, time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC), ny, "2026-03-08T03",
			time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC), time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC)},
		// The ISO week of the change is 167 hours long.
		{"week of the spring dst change", Week, time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), ny, "2026-W10",
			time.Date(2026, 3, 2, 5, 0, 0, 0, time.UTC), time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)},
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
	for _, bad := range []string{"", "0000", "20x6", "+026", "al", "ALL", "2026-W00", "2026-W54", "2025-W53", "2026-11-31", "2026-02-29", "2026-11-05T24", "2026-11-05 13", "2026-11-0a", "2026-W+1", "2026-13-01"} {
		if _, _, _, err := WindowBounds(bad, time.UTC); err == nil {
			t.Errorf("WindowBounds(%q) accepted", bad)
		}
	}
}

func TestParseWindow(t *testing.T) {
	for in, want := range map[string]Window{"": Month, "month": Month, "hour": Hour, "day": Day, "week": Week, "year": Year, "lifetime": Lifetime} {
		if got, ok := ParseWindow(in); !ok || got != want {
			t.Errorf("ParseWindow(%q) = %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"yearly", "forever", "Month", "Year"} {
		if _, ok := ParseWindow(bad); ok {
			t.Errorf("ParseWindow(%q) accepted", bad)
		}
	}
}

func TestLifetimeWindow(t *testing.T) {
	for _, loc := range []*time.Location{time.UTC, mustLoc(t, "Europe/Istanbul")} {
		id, start, end := WindowOf(Lifetime, time.Date(2026, 11, 5, 21, 30, 0, 0, time.UTC), loc)
		if id != "all" || !start.IsZero() || !end.IsZero() {
			t.Fatalf("WindowOf(Lifetime) = %s %s %s, want all with no bounds", id, start, end)
		}
		w, bs, be, err := WindowBounds("all", loc)
		if err != nil || w != Lifetime || !bs.IsZero() || !be.IsZero() {
			t.Fatalf("WindowBounds(all) = %s %s %s %v", w, bs, be, err)
		}
	}
	// It never rolls over: any two instants are in the same lifetime window.
	a, _, _ := WindowOf(Lifetime, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	b, _, _ := WindowOf(Lifetime, time.Date(2090, 6, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	if a != b {
		t.Fatalf("lifetime ids differ: %s %s", a, b)
	}
}

// A year rolls over at the tenant's midnight of 1 January, not UTC's: the same instant is two different years for two tenants.
func TestYearRolloverFollowsTheTenantZone(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	ny := mustLoc(t, "America/New_York")
	at := time.Date(2026, 12, 31, 22, 0, 0, 0, time.UTC) // 01:00 on 1 Jan in Istanbul, 17:00 on 31 Dec in New York
	if id, _, _ := WindowOf(Year, at, ist); id != "2027" {
		t.Errorf("istanbul year = %s, want 2027", id)
	}
	if id, _, _ := WindowOf(Year, at, ny); id != "2026" {
		t.Errorf("new york year = %s, want 2026", id)
	}
	// Consecutive years tile the time line without a gap or an overlap.
	_, _, end := WindowOf(Year, at.Add(-24*time.Hour), ist)
	id, start, _ := WindowOf(Year, end, ist)
	if !start.Equal(end) || id != "2027" {
		t.Errorf("the year starting at %s is %s from %s", end, id, start)
	}
}

func TestSpan(t *testing.T) {
	cases := []struct {
		id       string
		g        Granule
		from, to string
	}{
		{"2026-11", Months, "2026-11", "2026-11"},
		{"2026", Months, "2026-01", "2026-12"},
		{"all", Months, "0000-01", "9999-12"},
		{"2026-11-05T13", Hours, "2026-11-05T13", "2026-11-05T13"},
		{"2026-11-05", Hours, "2026-11-05T00", "2026-11-05T23"},
		{"2026-W45", Hours, "2026-11-02T00", "2026-11-08T23"},
		{"2026-W53", Hours, "2026-12-28T00", "2027-01-03T23"},
		{"2028-02-29", Hours, "2028-02-29T00", "2028-02-29T23"},
	}
	for _, c := range cases {
		g, from, to, err := Span(c.id)
		if err != nil || g != c.g || from != c.from || to != c.to {
			t.Errorf("Span(%q) = %v %q %q %v, want %v %q %q", c.id, g, from, to, err, c.g, c.from, c.to)
		}
	}
	if _, _, _, err := Span("2026-W54"); err == nil {
		t.Error("Span accepted a malformed id")
	}
}

// Every hour of a window has an id inside the window's span, so summing the stored rows by span counts each event once, DST days included.
func TestSpanCoversEveryHourOfTheWindow(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	for _, day := range []time.Time{time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)} {
		for _, w := range []Window{Day, Week, Month, Year} {
			id, start, end := WindowOf(w, day, ny)
			g, from, to, err := Span(id)
			if err != nil {
				t.Fatal(err)
			}
			for at := start; at.Before(end); at = at.Add(time.Hour) {
				hid, _, _ := WindowOf(Hour, at, ny)
				key := hid
				if g == Months {
					key = hid[:7]
				}
				if key < from || key > to {
					t.Fatalf("%s window %s: hour %s (%s) is outside [%s, %s]", w, id, hid, at.UTC(), from, to)
				}
			}
		}
	}
}
