package webhook

import "testing"

// A preview of a change at the end of the period must say so; it used to answer "immediately" whatever was asked.
func TestPreviewTimingFollowsTheRequest(t *testing.T) {
	for in, want := range map[string]string{
		"immediately": "CHANGE_TIMING_IMMEDIATELY",
		"":            "CHANGE_TIMING_IMMEDIATELY",
		"period_end":  "CHANGE_TIMING_PERIOD_END",
	} {
		if got := previewTiming(in); got != want {
			t.Errorf("previewTiming(%q) = %s; want %s", in, got, want)
		}
	}
}
