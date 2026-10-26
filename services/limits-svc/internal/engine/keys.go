// Package engine applies usage events to Redis counters, fires threshold events and reconciles the counters
// against the aggregator.
package engine

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Redis keys (docs/03-services/limits-svc.md). All keys of a batch are touched by one script on one node; in
// Redis Cluster they would need a shared hash tag per partition, which a single node doesn't.
//
// lim:c:… counts a billing period (a month window) and is reconciled against the aggregator. lim:w:… counts a shorter window
// (an hour, a day, an ISO week) that the aggregator does not keep, for an entitlement with that window; it is never reconciled.
func CounterKey(tenant, customer uuid.UUID, meter, period string) string {
	return fmt.Sprintf("lim:c:%s:%s:%s:%s", tenant, customer, meter, period)
}

// WindowKey is the counter of a window shorter than a month; window is its id ("2026-11-05T13", "2026-11-05", "2026-W45").
func WindowKey(tenant, customer uuid.UUID, meter, window string) string {
	return fmt.Sprintf("lim:w:%s:%s:%s:%s", tenant, customer, meter, window)
}

func OffsetKey(p int32) string { return fmt.Sprintf("lim:off:%d", p) }
func ActiveKey(p int32) string { return fmt.Sprintf("lim:active:%d", p) }

// WindowActiveKey is the sorted set of the window counters of partition p (score = last update): the threshold sweep reads it, reconciliation does not.
func WindowActiveKey(p int32) string { return fmt.Sprintf("lim:wactive:%d", p) }
func TSKey(tenant uuid.UUID) string {
	return "lim:ts:" + tenant.String()
}
func FiredKey(tenant, customer uuid.UUID, meter, period string, pct int32) string {
	return fmt.Sprintf("lim:fired:%s:%s:%s:%s:%d", tenant, customer, meter, period, pct)
}

// Ref is the parsed form of a counter key.
type Ref struct {
	Tenant, Customer uuid.UUID
	// Period is the id of the window counted: a billing period, or a shorter window when Windowed.
	Meter, Period string
	// Windowed marks a lim:w counter.
	Windowed bool
}

// ParseCounterKey splits a billing-period counter key (lim:c). Meter codes and window ids never contain ':'.
func ParseCounterKey(k string) (Ref, bool) {
	r, ok := ParseKey(k)
	return r, ok && !r.Windowed
}

// ParseKey splits a counter key of either kind.
func ParseKey(k string) (Ref, bool) {
	parts := strings.Split(k, ":")
	if len(parts) != 6 || parts[0] != "lim" || (parts[1] != "c" && parts[1] != "w") {
		return Ref{}, false
	}
	t, err1 := uuid.Parse(parts[2])
	c, err2 := uuid.Parse(parts[3])
	if err1 != nil || err2 != nil {
		return Ref{}, false
	}
	return Ref{Tenant: t, Customer: c, Meter: parts[4], Period: parts[5], Windowed: parts[1] == "w"}, true
}

// Key renders the counter key of the ref.
func (r Ref) Key() string {
	if r.Windowed {
		return WindowKey(r.Tenant, r.Customer, r.Meter, r.Period)
	}
	return CounterKey(r.Tenant, r.Customer, r.Meter, r.Period)
}
