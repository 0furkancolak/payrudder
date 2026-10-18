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

// Bounds returns [start, end) of the period id in loc.
func Bounds(id string, loc *time.Location) (start, end time.Time, err error) {
	var y, m int
	if n, err := fmt.Sscanf(id, "%4d-%2d", &y, &m); err != nil || n != 2 || m < 1 || m > 12 || len(id) != 7 {
		return time.Time{}, time.Time{}, fmt.Errorf("period: malformed id %q", id)
	}
	start = time.Date(y, time.Month(m), 1, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 1, 0), nil
}
