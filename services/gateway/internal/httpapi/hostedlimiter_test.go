package httpapi

import (
	"strconv"
	"testing"
	"time"
)

// A public page limiter keeps one entry per client address for a minute. Addresses that all asked within that minute are all fresh, so nothing is old enough
// to sweep: the table used to grow without a bound (and, over the bound, to be scanned end to end on every request).
func TestHostedLimiterTableHasABound(t *testing.T) {
	var l hostedLimiter
	now := time.Now()
	for i := range hostedMaxKeys + 5000 {
		if !l.allow("10.0."+strconv.Itoa(i), now) {
			t.Fatalf("the first request of address %d was refused", i)
		}
	}
	if n := len(l.window); n > hostedMaxKeys {
		t.Errorf("the table holds %d addresses, want at most %d", n, hostedMaxKeys)
	}
	// What was counted is still counted: an address over its limit stays over it.
	for range hostedPerMinute {
		l.allow("busy", now)
	}
	if l.allow("busy", now) {
		t.Error("the limit of a counted address was forgotten")
	}
}
