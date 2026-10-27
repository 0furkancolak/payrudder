package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/usage"
)

func TestHourCellsMergeLikeTheMonthRows(t *testing.T) {
	tn, cust := uuid.New(), uuid.New()
	d := NewDelta()
	ev := func(units int64) usage.Event {
		return usage.Event{TenantID: tn, CustomerID: cust, Meter: "m", Units: units, EventTime: time.Now()}
	}
	sum, peak := usage.Meter{Code: "m", Aggregation: usage.Sum}, usage.Meter{Code: "m", Aggregation: usage.Max}
	k := HourKey{Tenant: tn, Customer: cust, Meter: "m", Hour: "2026-11-05T13", Generation: 1}
	for _, u := range []int64{4, 9, 2} {
		d.AddHour(k, sum, ev(u))
	}
	if c := d.Hours[k]; c.Value.Int64() != 15 || c.Events != 3 {
		t.Fatalf("sum cell = %v over %d events, want 15 over 3", c.Value, c.Events)
	}
	k2 := HourKey{Tenant: tn, Customer: cust, Meter: "m", Hour: "2026-11-05T14", Generation: 1}
	for _, u := range []int64{4, 9, 2} {
		d.AddHour(k2, peak, ev(u))
	}
	if c := d.Hours[k2]; c.Value.Int64() != 9 || c.Events != 3 {
		t.Fatalf("max cell = %v over %d events, want 9 over 3", c.Value, c.Events)
	}
	// Locks are taken in key order, so two batches never wait on each other in a cycle.
	k0 := HourKey{Tenant: tn, Customer: cust, Meter: "m", Hour: "2026-11-05T09", Generation: 2}
	d.AddHour(k0, sum, ev(1))
	got := d.SortedHours()
	if len(got) != 3 || got[0] != k0 || got[1] != k || got[2] != k2 {
		t.Fatalf("sorted = %v", got)
	}
}
