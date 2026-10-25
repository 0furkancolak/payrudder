package query

import (
	"slices"
	"testing"
	"time"
)

// A tenant that set no delays is shown the ones the service runs on, in the unit it would set them in: hours for a live tenant, minutes for a sandbox one.
func TestRetryHoursShowsTheDelaysInTheTenantsUnit(t *testing.T) {
	live := []time.Duration{2 * time.Hour, 5 * time.Hour, 48 * time.Hour}
	if got := retryHours(live, false); !slices.Equal(got, []int32{2, 5, 48}) {
		t.Errorf("live = %v, want [2 5 48]", got)
	}
	sandbox := []time.Duration{30 * time.Second * 2, 3 * time.Minute, 90 * time.Minute}
	if got := retryHours(sandbox, true); !slices.Equal(got, []int32{1, 3, 90}) {
		t.Errorf("sandbox = %v, want [1 3 90]", got)
	}
	if got := retryHours(nil, false); len(got) != 0 {
		t.Errorf("no delays = %v, want none", got)
	}
}
