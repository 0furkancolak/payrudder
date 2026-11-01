package command

import "testing"

// Asking again for a failed refund carries the same key unless the provider refused it: a refund that may have been made must not be asked for under a new key.
func TestMayHaveHappened(t *testing.T) {
	for refundError, want := range map[string]bool{
		noAnswer + " after 3 attempts":                                    true,
		"TIMEOUT: the provider did not answer in time":                    true,
		"REFUND_OUTCOME_UNKNOWN: iyzico was asked for this refund before": true,
		"REJECTED: iyzico cannot refund this":                             false,
		"REJECTED: TIMEOUT: a refusal that quotes a timeout":              false,
		"PROVIDER_NOT_CONFIGURED: no live payment provider":               false,
		"": false,
	} {
		if got := mayHaveHappened(refundError); got != want {
			t.Errorf("mayHaveHappened(%q) = %v, want %v", refundError, got, want)
		}
	}
}
