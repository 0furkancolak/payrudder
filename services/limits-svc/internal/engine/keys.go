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
func CounterKey(tenant, customer uuid.UUID, meter, period string) string {
	return fmt.Sprintf("lim:c:%s:%s:%s:%s", tenant, customer, meter, period)
}

func OffsetKey(p int32) string { return fmt.Sprintf("lim:off:%d", p) }
func ActiveKey(p int32) string { return fmt.Sprintf("lim:active:%d", p) }
func TSKey(tenant uuid.UUID) string {
	return "lim:ts:" + tenant.String()
}
func FiredKey(tenant, customer uuid.UUID, meter, period string, pct int32) string {
	return fmt.Sprintf("lim:fired:%s:%s:%s:%s:%d", tenant, customer, meter, period, pct)
}

// Ref is the parsed form of a counter key.
type Ref struct {
	Tenant, Customer uuid.UUID
	Meter, Period    string
}

// ParseCounterKey splits a counter key. Meter codes and periods never contain ':'.
func ParseCounterKey(k string) (Ref, bool) {
	parts := strings.Split(k, ":")
	if len(parts) != 6 || parts[0] != "lim" || parts[1] != "c" {
		return Ref{}, false
	}
	t, err1 := uuid.Parse(parts[2])
	c, err2 := uuid.Parse(parts[3])
	if err1 != nil || err2 != nil {
		return Ref{}, false
	}
	return Ref{Tenant: t, Customer: c, Meter: parts[4], Period: parts[5]}, true
}

// Key renders the counter key of the ref.
func (r Ref) Key() string { return CounterKey(r.Tenant, r.Customer, r.Meter, r.Period) }
