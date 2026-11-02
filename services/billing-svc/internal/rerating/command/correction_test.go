package command

import (
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
)

// A re-rated period whose trial ended inside it prices the fixed fee the way the target version bills it: from the end of the trial when it prorates, in full when not.
func TestTargetForChargesTheFeeFromTheEndOfTheTrial(t *testing.T) {
	start, end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	trialEnd := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC) // 20 of the 31 days are left
	sub := inputs.Subscription{TrialEnd: &trialEnd}
	for _, tc := range []struct {
		name string
		pv   rating.PlanVersion
		sub  inputs.Subscription
		want int64
	}{
		{"prorating version", rating.PlanVersion{BaseFeeMinor: 1000, ProrateAfterTrial: true}, sub, 645},
		{"version that charges the whole period", rating.PlanVersion{BaseFeeMinor: 1000}, sub, 1000},
		{"no trial", rating.PlanVersion{BaseFeeMinor: 1000, ProrateAfterTrial: true}, inputs.Subscription{}, 1000},
	} {
		got, err := targetFor(tc.pv, tc.sub, start, end)
		if err != nil || got.BaseFeeMinor != tc.want {
			t.Errorf("%s: base fee = %d, %v; want %d", tc.name, got.BaseFeeMinor, err, tc.want)
		}
	}
}
