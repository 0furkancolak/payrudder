package httpapi

import (
	"testing"

	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

const keyRateEvent = `{"events":[{"customer_external_id":"c","meter_code":"m","quantity":"1","event_time":"2026-11-03T10:15:00Z"}]}`

// A key with a share of its own is charged in its own bucket first (with its percentage), then the tenant's; a key without one never reaches AllowKey.
func TestKeyPercentReachesTheLimiter(t *testing.T) {
	ts, _, _, lim := newFullTestServer(t)
	if resp, m := do(t, ts, "POST", "/v1/events", "pct-key", keyRateEvent); resp.StatusCode >= 300 {
		t.Fatalf("pct-key: %d %v", resp.StatusCode, m)
	}
	if len(lim.keyPercents) != 1 || lim.keyPercents[0] != 20 || len(lim.costs[ratelimit.ClassIngest]) != 1 {
		t.Fatalf("key calls %v, tenant charges %v; want one of each, the key's at 20", lim.keyPercents, lim.costs)
	}
	do(t, ts, "POST", "/v1/events", "keyed-key", keyRateEvent)
	if len(lim.keyPercents) != 1 {
		t.Fatalf("a key without a percentage was charged in its own bucket: %v", lim.keyPercents)
	}
}

// A key that has spent its share is refused with the same 429 as a tenant, and the tenant's bucket is not charged.
func TestKeyOverItsShareIsRefusedBeforeTheTenant(t *testing.T) {
	ts, _, ing, lim := newFullTestServer(t)
	resp, m := do(t, ts, "POST", "/v1/events", "over-key", keyRateEvent)
	if resp.StatusCode != 429 || errKeyOf(m) != "gateway.rate_limit.exceeded" || resp.Header.Get("Retry-After") != "3" {
		t.Fatalf("got %d retry=%q %v", resp.StatusCode, resp.Header.Get("Retry-After"), m)
	}
	if len(lim.costs[ratelimit.ClassIngest]) != 0 || ing.last != nil {
		t.Fatalf("the tenant was charged (%v) or the request reached ingest-svc", lim.costs)
	}
}
