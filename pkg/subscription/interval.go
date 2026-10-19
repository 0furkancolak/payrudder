package subscription

import (
	"fmt"
	"time"
)

// Interval is the length of one billing period.
type Interval string

const (
	Monthly Interval = "month"
	Yearly  Interval = "year"
)

// Valid reports whether i is a supported interval.
func (i Interval) Valid() bool { return i == Monthly || i == Yearly }

// PeriodEnd returns the end (exclusive) of the period that starts at start and is the n-th period (n = 0 for the first)
// of a subscription anchored at anchor, computed in loc. Months are counted from the anchor, not from the previous end, so a
// subscription anchored on 31 January ends its periods on 28 February, then 31 March: the day of month is clamped
// to the month's length and never drifts downwards.
func PeriodEnd(anchor time.Time, i Interval, n int, loc *time.Location) (time.Time, error) {
	if !i.Valid() {
		return time.Time{}, fmt.Errorf("subscription: unknown interval %q", i)
	}
	if n < 0 {
		return time.Time{}, fmt.Errorf("subscription: period index %d is negative", n)
	}
	months := n + 1
	if i == Yearly {
		months = 12 * (n + 1)
	}
	return addMonthsClamped(anchor.In(loc), months), nil
}

// PeriodStart is PeriodEnd of the previous period, or the anchor for the first.
func PeriodStart(anchor time.Time, i Interval, n int, loc *time.Location) (time.Time, error) {
	if n == 0 {
		if !i.Valid() {
			return time.Time{}, fmt.Errorf("subscription: unknown interval %q", i)
		}
		return anchor.In(loc), nil
	}
	return PeriodEnd(anchor, i, n-1, loc)
}

// PeriodIndex returns the index of the period that contains t, or an error before the anchor.
func PeriodIndex(anchor time.Time, i Interval, t time.Time, loc *time.Location) (int, error) {
	if t.Before(anchor) {
		return 0, fmt.Errorf("subscription: %s is before the anchor %s", t, anchor)
	}
	for n := 0; ; n++ {
		end, err := PeriodEnd(anchor, i, n, loc)
		if err != nil {
			return 0, err
		}
		if t.Before(end) {
			return n, nil
		}
	}
}

func addMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	target := time.Date(y, m+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
	if d > last {
		d = last
	}
	return time.Date(target.Year(), target.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}
