// Package engine applies usage events to Redis counters, fires threshold events and reconciles the counters
// against the aggregator.
package engine

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/period"
)

// Redis keys (docs/03-services/limits-svc.md). All keys of a batch are touched by one script on one node; in
// Redis Cluster they would need a shared hash tag per partition, which a single node doesn't.
//
// lim:c:… counts a billing period (a month window). lim:w:… counts a short window (an hour, a day, an ISO week) and lim:y:… a long one (a
// year, the lifetime), for an entitlement with that window. All three are reconciled against the aggregator; they differ in how long they
// are watched: lim:c and lim:y are listed in lim:active:{p} (45 days), lim:w in lim:wactive:{p} (an hour, then the window has moved on).
func CounterKey(tenant, customer uuid.UUID, meter, period string) string {
	return fmt.Sprintf("lim:c:%s:%s:%s:%s", tenant, customer, meter, period)
}

// WindowKey is the counter of a window other than a billing period; window is its id ("2026-11-05T13", "2026-11-05", "2026-W45",
// "2026", "all"). A long window (year, lifetime) has its own key prefix so that the apply script can tell it from a short one.
func WindowKey(tenant, customer uuid.UUID, meter, window string) string {
	prefix := "w"
	if LongWindow(window) {
		prefix = "y"
	}
	return fmt.Sprintf("lim:%s:%s:%s:%s:%s", prefix, tenant, customer, meter, window)
}

// LongWindow reports whether the window id is a year ("2026") or the lifetime ("all"): counters that stay worth reconciling for as long as
// they live, not only while they are being updated.
func LongWindow(id string) bool { return id == period.LifetimeID || len(id) == 4 }

// SetKey is the set of the distinct values behind a UNIQUE counter, and OrderKey the "micros:id" of the event a LAST counter holds. Both are derived
// from the counter key (so they have more than six parts and never parse as a counter) and share its expiry.
func SetKey(counter string) string   { return "lim:set:" + strings.TrimPrefix(counter, "lim:") }
func OrderKey(counter string) string { return "lim:ord:" + strings.TrimPrefix(counter, "lim:") }

func OffsetKey(p int32) string { return fmt.Sprintf("lim:off:%d", p) }
func ActiveKey(p int32) string { return fmt.Sprintf("lim:active:%d", p) }

// WindowActiveKey is the sorted set of the short-window counters (lim:w) of partition p (score = last update): the threshold sweep and the
// reconciliation of recent windows read it.
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
	// Period is the id of the window counted: a billing period, or another window when Windowed.
	Meter, Period string
	// Windowed marks a lim:w or lim:y counter.
	Windowed bool
}

// ParseKey splits a counter key of any kind. Meter codes and window ids never contain ':'. A prefix that does not fit the window id (a year
// under lim:w) is refused.
func ParseKey(k string) (Ref, bool) {
	parts := strings.Split(k, ":")
	if len(parts) != 6 || parts[0] != "lim" || (parts[1] != "c" && parts[1] != "w" && parts[1] != "y") {
		return Ref{}, false
	}
	if (parts[1] == "y") != LongWindow(parts[5]) && parts[1] != "c" {
		return Ref{}, false
	}
	t, err1 := uuid.Parse(parts[2])
	c, err2 := uuid.Parse(parts[3])
	if err1 != nil || err2 != nil {
		return Ref{}, false
	}
	return Ref{Tenant: t, Customer: c, Meter: parts[4], Period: parts[5], Windowed: parts[1] != "c"}, true
}

// Key renders the counter key of the ref.
func (r Ref) Key() string {
	if r.Windowed {
		return WindowKey(r.Tenant, r.Customer, r.Meter, r.Period)
	}
	return CounterKey(r.Tenant, r.Customer, r.Meter, r.Period)
}
