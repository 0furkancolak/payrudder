// Package period maps instants to billing periods. Periods are calendar months in the tenant's time zone,
// identified as "YYYY-MM". The time zone is immutable per tenant, so a period_id stamped at ingest never changes
// under replay.
package period

import (
	"fmt"
	"time"
)

// PeriodOf returns the period id of t in loc, e.g. "2026-11".
func PeriodOf(t time.Time, loc *time.Location) string {
	y, m, _ := t.In(loc).Date()
	return fmt.Sprintf("%04d-%02d", y, int(m))
}

// Valid reports whether id is exactly "YYYY-MM": four ASCII digits, a dash and a month 01 to 12. It is the one
// definition of a well-formed period id; services must not keep their own pattern.
func Valid(id string) bool {
	if len(id) != 7 || id[4] != '-' {
		return false
	}
	for i := 0; i < 7; i++ {
		if i != 4 && (id[i] < '0' || id[i] > '9') {
			return false
		}
	}
	return id[5] == '0' && id[6] != '0' || id[5] == '1' && id[6] <= '2'
}

// Parse returns the year and month of a period id, or an error when !Valid(id).
func Parse(id string) (year, month int, err error) {
	if !Valid(id) {
		return 0, 0, fmt.Errorf("period: malformed id %q", id)
	}
	year = int(id[0]-'0')*1000 + int(id[1]-'0')*100 + int(id[2]-'0')*10 + int(id[3]-'0')
	month = int(id[5]-'0')*10 + int(id[6]-'0')
	return year, month, nil
}

// Bounds returns [start, end) of the period id in loc.
func Bounds(id string, loc *time.Location) (start, end time.Time, err error) {
	y, m, err := Parse(id)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start = time.Date(y, time.Month(m), 1, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 1, 0), nil
}

// Cutoff returns the instant after which events of a period count as late: the end of the period plus the grace.
func Cutoff(id string, loc *time.Location, grace time.Duration) (time.Time, error) {
	_, end, err := Bounds(id, loc)
	if err != nil {
		return time.Time{}, err
	}
	return end.Add(grace), nil
}

// Class says whether an event counts in its own period or as late usage.
type Class int

const (
	// OnTime events were ingested before the cutoff and aggregate into their own period.
	OnTime Class = iota
	// Late events were ingested at or after the cutoff and are billed in the period of their ingestion.
	Late
)

func (c Class) String() string {
	if c == OnTime {
		return "on_time"
	}
	return "late"
}

// Classify is deterministic in its inputs, so replays reproduce it: on time strictly before the cutoff.
func Classify(ingestedAt, cutoff time.Time) Class {
	if ingestedAt.Before(cutoff) {
		return OnTime
	}
	return Late
}
