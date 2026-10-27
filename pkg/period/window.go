package period

import (
	"fmt"
	"strconv"
	"time"
)

// Window is what a quota counts over before it starts again. Windows are wall-clock spans in the tenant's time zone, like billing
// periods: a month window is exactly a billing period, and a year is twelve of them (January to December). A lifetime window never
// starts again.
type Window string

const (
	Hour     Window = "hour"
	Day      Window = "day"
	Week     Window = "week"
	Month    Window = "month"
	Year     Window = "year"
	Lifetime Window = "lifetime"
)

// Windows lists every window, shortest first.
var Windows = []Window{Hour, Day, Week, Month, Year, Lifetime}

// LifetimeID is the id of the one lifetime window.
const LifetimeID = "all"

// ParseWindow reads a stored or wire window; "" is Month (the billing period, as before windows existed).
func ParseWindow(s string) (Window, bool) {
	switch Window(s) {
	case "", Month:
		return Month, true
	case Hour, Day, Week, Year, Lifetime:
		return Window(s), true
	default:
		return "", false
	}
}

// WindowOf returns the id and [start, end) of the window of kind w that holds t in loc. The ids never contain ':' and differ in
// shape per kind, so one id names its kind too: "2026-11-05T13" (hour), "2026-11-05" (day), "2026-W45" (ISO week), "2026-11" (month),
// "2026" (year) and "all" (lifetime). The lifetime window has no bounds: start and end are the zero time.
func WindowOf(w Window, t time.Time, loc *time.Location) (id string, start, end time.Time) {
	lt := t.In(loc)
	y, m, d := lt.Date()
	switch w {
	case Hour:
		start = time.Date(y, m, d, lt.Hour(), 0, 0, 0, loc)
		end = time.Date(y, m, d, lt.Hour()+1, 0, 0, 0, loc)
		if !end.After(start) { // a wall-clock hour skipped or repeated by a DST change
			end = start.Add(time.Hour)
		}
		return fmt.Sprintf("%04d-%02d-%02dT%02d", y, int(m), d, lt.Hour()), start, end
	case Day:
		start = time.Date(y, m, d, 0, 0, 0, 0, loc)
		return fmt.Sprintf("%04d-%02d-%02d", y, int(m), d), start, start.AddDate(0, 0, 1)
	case Week:
		back := (int(lt.Weekday()) + 6) % 7 // days since Monday
		start = time.Date(y, m, d-back, 0, 0, 0, 0, loc)
		iy, iw := start.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", iy, iw), start, start.AddDate(0, 0, 7)
	case Year:
		start = time.Date(y, 1, 1, 0, 0, 0, 0, loc)
		return fmt.Sprintf("%04d", y), start, start.AddDate(1, 0, 0)
	case Lifetime:
		return LifetimeID, time.Time{}, time.Time{}
	default:
		start = time.Date(y, m, 1, 0, 0, 0, 0, loc)
		return PeriodOf(t, loc), start, start.AddDate(0, 1, 0)
	}
}

// WindowBounds returns the kind and [start, end) of a window id made by WindowOf; the lifetime window's bounds are the zero time.
func WindowBounds(id string, loc *time.Location) (w Window, start, end time.Time, err error) {
	bad := func() (Window, time.Time, time.Time, error) {
		return "", time.Time{}, time.Time{}, fmt.Errorf("period: malformed window id %q", id)
	}
	num := func(s string) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil && len(s) > 0 && s[0] != '-' && s[0] != '+'
	}
	switch {
	case id == LifetimeID:
		return Lifetime, time.Time{}, time.Time{}, nil
	case len(id) == 4:
		y, ok := num(id)
		if !ok || y < 1 {
			return bad()
		}
		start = time.Date(y, 1, 1, 0, 0, 0, 0, loc)
		return Year, start, start.AddDate(1, 0, 0), nil
	case Valid(id):
		start, end, err = Bounds(id, loc)
		return Month, start, end, err
	case len(id) == 8 && id[4:6] == "-W":
		y, ok1 := num(id[:4])
		wk, ok2 := num(id[6:])
		if !ok1 || !ok2 || wk < 1 || wk > 53 {
			return bad()
		}
		// The Monday of ISO week 1 is the Monday of the week holding January 4th.
		jan4 := time.Date(y, 1, 4, 0, 0, 0, 0, loc)
		monday := jan4.AddDate(0, 0, -((int(jan4.Weekday())+6)%7)+(wk-1)*7)
		if iy, iw := monday.ISOWeek(); iy != y || iw != wk {
			return bad()
		}
		return Week, monday, monday.AddDate(0, 0, 7), nil
	case len(id) == 10 || len(id) == 13:
		if !Valid(id[:7]) || id[7] != '-' {
			return bad()
		}
		y, mo, _ := Parse(id[:7])
		d, ok := num(id[8:10])
		if !ok || d < 1 || d > daysIn(y, mo) {
			return bad()
		}
		if len(id) == 10 {
			start = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, loc)
			return Day, start, start.AddDate(0, 0, 1), nil
		}
		h, ok := num(id[11:])
		if id[10] != 'T' || !ok || h > 23 {
			return bad()
		}
		_, start, end = WindowOf(Hour, time.Date(y, time.Month(mo), d, h, 0, 0, 0, loc), loc)
		return Hour, start, end, nil
	default:
		return bad()
	}
}

func daysIn(y, m int) int { return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// Granule is the grain a window's usage is stored in: the aggregator keeps a total per month and, for short windows, per hour.
type Granule int

const (
	Months Granule = iota
	Hours
)

// Span returns the stored rows that add up to the window id: the ids from..to, both inclusive, of the rows of that granule ("2026-11"
// for a month, "2026-11-05T13" for an hour). Hour ids are wall-clock strings of the tenant, so they sort as time does and need no zone.
// A year is its twelve months and the lifetime window every month there is.
func Span(id string) (g Granule, from, to string, err error) {
	w, start, end, err := WindowBounds(id, time.UTC)
	if err != nil {
		return 0, "", "", err
	}
	const hourLayout = "2006-01-02T15"
	switch w {
	case Month:
		return Months, id, id, nil
	case Year:
		return Months, fmt.Sprintf("%04d-01", start.Year()), fmt.Sprintf("%04d-12", start.Year()), nil
	case Lifetime:
		return Months, "0000-01", "9999-12", nil
	case Hour:
		return Hours, id, id, nil
	default: // day, week
		return Hours, start.Format(hourLayout), end.Add(-time.Hour).Format(hourLayout), nil
	}
}
