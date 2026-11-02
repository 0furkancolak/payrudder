package model

import (
	"testing"
	"time"

	_ "github.com/0furkancolak/payrudder/pkg/tzdata"
)

// An invoice number's year is the year it is in at the tenant's wall clock (docs/plans/05): an invoice finalized in the first hours of 1 January in Istanbul is the
// first of the new year's series although UTC is still on 31 December. No zone (a draft made before the zone was frozen into its snapshot) or a zone that does not load
// is UTC, the rule every number had until then.
func TestNumberYearIsTheYearInTheTenantsZone(t *testing.T) {
	newYearInIstanbul := time.Date(2026, 12, 31, 22, 30, 0, 0, time.UTC) // 01:30 on 1 January 2027 in Istanbul, 12:30 on the 31st in Honolulu
	for _, tc := range []struct {
		zone string
		at   time.Time
		want int32
	}{
		{"Europe/Istanbul", newYearInIstanbul, 2027},
		{"Pacific/Honolulu", newYearInIstanbul, 2026},
		{"", newYearInIstanbul, 2026},
		{"UTC", newYearInIstanbul, 2026},
		{"Not/AZone", newYearInIstanbul, 2026},
		{"Pacific/Honolulu", time.Date(2027, 1, 1, 9, 59, 59, 0, time.UTC), 2026}, // 23:59:59 on the 31st there
		{"Pacific/Honolulu", time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC), 2027},
		{"Europe/Istanbul", time.Date(2026, 12, 31, 20, 59, 59, 0, time.UTC), 2026}, // 23:59:59 on the 31st in Istanbul (UTC+3)
		{"Europe/Istanbul", time.Date(2026, 12, 31, 21, 0, 0, 0, time.UTC), 2027},
	} {
		if got := NumberYear(tc.at, tc.zone); got != tc.want {
			t.Errorf("NumberYear(%s, %q) = %d, want %d", tc.at.Format(time.RFC3339), tc.zone, got, tc.want)
		}
	}
}
