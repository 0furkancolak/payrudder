package period

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestPeriodOfMonthBoundaries(t *testing.T) {
	cases := []struct {
		name string
		tz   string
		at   time.Time
		want string
	}{
		// Istanbul is UTC+3 all year: 21:00Z on the 31st is already the 1st locally.
		{"istanbul just after local midnight", "Europe/Istanbul", time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC), "2026-11"},
		{"istanbul just before local midnight", "Europe/Istanbul", time.Date(2026, 10, 31, 20, 59, 59, 0, time.UTC), "2026-10"},
		// New York: EDT (UTC-4) until Nov 1 2026 02:00 local; the month boundary is 04:00Z on Nov 1.
		{"new york before boundary", "America/New_York", time.Date(2026, 11, 1, 3, 59, 59, 0, time.UTC), "2026-10"},
		{"new york at boundary", "America/New_York", time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC), "2026-11"},
		// DST ends Nov 1 2026 06:00Z (02:00 EDT -> 01:00 EST); Dec 1 boundary is 05:00Z (EST, UTC-5).
		{"new york december before", "America/New_York", time.Date(2026, 12, 1, 4, 59, 59, 0, time.UTC), "2026-11"},
		{"new york december at", "America/New_York", time.Date(2026, 12, 1, 5, 0, 0, 0, time.UTC), "2026-12"},
		// DST starts Mar 8 2026; the month boundary on Apr 1 is 04:00Z (EDT).
		{"new york april", "America/New_York", time.Date(2026, 4, 1, 3, 59, 59, 0, time.UTC), "2026-03"},
		{"new york april at", "America/New_York", time.Date(2026, 4, 1, 4, 0, 0, 0, time.UTC), "2026-04"},
		// Kiritimati is UTC+14: the local month starts 14h before UTC's.
		{"kiritimati ahead of utc", "Pacific/Kiritimati", time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC), "2026-11"},
		{"kiritimati before", "Pacific/Kiritimati", time.Date(2026, 10, 31, 9, 59, 59, 0, time.UTC), "2026-10"},
		{"kiritimati year end", "Pacific/Kiritimati", time.Date(2026, 12, 31, 10, 0, 0, 0, time.UTC), "2027-01"},
		{"utc", "UTC", time.Date(2026, 2, 28, 23, 59, 59, 0, time.UTC), "2026-02"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PeriodOf(c.at, mustLoc(t, c.tz)); got != c.want {
				t.Fatalf("PeriodOf(%s, %s) = %s, want %s", c.at, c.tz, got, c.want)
			}
		})
	}
}

func TestBoundsRoundTrip(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	start, end, err := Bounds("2026-11", loc)
	if err != nil {
		t.Fatal(err)
	}
	if PeriodOf(start, loc) != "2026-11" || PeriodOf(start.Add(-time.Nanosecond), loc) != "2026-10" {
		t.Fatalf("start %s is not the first instant of 2026-11", start)
	}
	if PeriodOf(end, loc) != "2026-12" || PeriodOf(end.Add(-time.Nanosecond), loc) != "2026-11" {
		t.Fatalf("end %s is not the first instant of 2026-12", end)
	}
	for _, bad := range []string{"", "2026-13", "2026-1", "26-11", "2026/11", "2026-111"} {
		if _, _, err := Bounds(bad, loc); err == nil {
			t.Errorf("Bounds(%q) accepted", bad)
		}
	}
}

func TestCutoffAndClassify(t *testing.T) {
	ist := mustLoc(t, "Europe/Istanbul")
	grace := 24 * time.Hour
	cutoff, err := Cutoff("2026-11", ist, grace)
	if err != nil {
		t.Fatal(err)
	}
	// November in Istanbul ends at 2026-11-30T21:00Z; one day of grace.
	if want := time.Date(2026, 12, 1, 21, 0, 0, 0, time.UTC); !cutoff.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", cutoff, want)
	}
	for _, c := range []struct {
		at   time.Time
		want Class
	}{
		{cutoff.Add(-time.Millisecond), OnTime},
		{cutoff, Late},
		{cutoff.Add(time.Millisecond), Late},
		{cutoff.Add(-time.Hour * 24 * 40), OnTime},
	} {
		if got := Classify(c.at, cutoff); got != c.want {
			t.Errorf("Classify(%s) = %s, want %s", c.at, got, c.want)
		}
	}

	// Year boundary, zero grace, a zone ahead of UTC and one behind.
	for _, tz := range []string{"Pacific/Kiritimati", "America/New_York"} {
		loc := mustLoc(t, tz)
		cut, err := Cutoff("2026-12", loc, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := PeriodOf(cut.Add(-time.Nanosecond), loc); got != "2026-12" {
			t.Errorf("%s: just before cutoff is %s", tz, got)
		}
		if got := PeriodOf(cut, loc); got != "2027-01" {
			t.Errorf("%s: at cutoff is %s", tz, got)
		}
		if Classify(cut.Add(-time.Millisecond), cut) != OnTime || Classify(cut, cut) != Late {
			t.Errorf("%s: classification around the year-end cutoff is wrong", tz)
		}
	}
	if _, err := Cutoff("nope", ist, 0); err == nil {
		t.Fatal("malformed period accepted")
	}
}
