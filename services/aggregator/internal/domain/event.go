// Package domain holds the aggregator's pure rules: the merge of
// events into aggregate cells. No I/O.
package domain

import (
	"math/big"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/usage"
)

// WindowRetention is how long the hour totals (usage_window_aggregates) are kept: the longest short window is a week, and a quota window is
// reconciled while it is current. Older hours are neither written nor kept.
const WindowRetention = 14 * 24 * time.Hour

// OnTimeKey identifies a usage_aggregates row.
type OnTimeKey struct {
	Tenant, Customer uuid.UUID
	Meter, Period    string
	Generation       int32
}

// HourKey identifies a usage_window_aggregates row; Hour is the wall-clock hour id of the tenant ("2026-11-05T13").
type HourKey struct {
	Tenant, Customer uuid.UUID
	Meter, Hour      string
	Generation       int32
}

// LateKey identifies a late_usage row.
type LateKey struct {
	Tenant, Customer uuid.UUID
	Meter            string
	Billed, Origin   string
	Generation       int32
}

// Cell is a pre-summed contribution to one row.
type Cell struct {
	Value       *big.Int // SUM/COUNT: the total to add. MAX: the maximum seen
	Scale       int
	Events      int64
	Aggregation usage.Aggregation
}

// Delta collects the pre-summed contributions of one batch (one cell per distinct row, whatever the number of events).
type Delta struct {
	OnTime map[OnTimeKey]*Cell
	// Hours are the hour totals of the on-time events, which limits-svc reconciles its short quota windows against.
	Hours map[HourKey]*Cell
	Late  map[LateKey]*Cell
}

// NewDelta returns an empty Delta.
func NewDelta() *Delta {
	return &Delta{OnTime: map[OnTimeKey]*Cell{}, Hours: map[HourKey]*Cell{}, Late: map[LateKey]*Cell{}}
}

func (c *Cell) add(v int64, scale int, agg usage.Aggregation) {
	if c.Value == nil {
		c.Value, c.Scale, c.Aggregation = big.NewInt(v), scale, agg
		c.Events = 1
		return
	}
	c.Events++
	if agg == usage.Max {
		if n := big.NewInt(v); n.Cmp(c.Value) > 0 {
			c.Value = n
		}
		return
	}
	c.Value.Add(c.Value, big.NewInt(v))
}

// AddOnTime merges an on-time event into the cell of its row.
func (d *Delta) AddOnTime(k OnTimeKey, m usage.Meter, e usage.Event) {
	c := d.OnTime[k]
	if c == nil {
		c = &Cell{}
		d.OnTime[k] = c
	}
	c.add(m.Value(e), e.Scale, m.Aggregation)
}

// AddHour merges an on-time event into the cell of its hour row.
func (d *Delta) AddHour(k HourKey, m usage.Meter, e usage.Event) {
	c := d.Hours[k]
	if c == nil {
		c = &Cell{}
		d.Hours[k] = c
	}
	c.add(m.Value(e), e.Scale, m.Aggregation)
}

// AddLate merges a late event into the cell of its row.
func (d *Delta) AddLate(k LateKey, m usage.Meter, e usage.Event) {
	c := d.Late[k]
	if c == nil {
		c = &Cell{}
		d.Late[k] = c
	}
	c.add(m.Value(e), e.Scale, m.Aggregation)
}

// SortedOnTime returns the keys in a fixed order, so that concurrent transactions lock rows in the same order.
func (d *Delta) SortedOnTime() []OnTimeKey {
	keys := make([]OnTimeKey, 0, len(d.OnTime))
	for k := range d.OnTime {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		switch {
		case a.Tenant != b.Tenant:
			return a.Tenant.String() < b.Tenant.String()
		case a.Period != b.Period:
			return a.Period < b.Period
		case a.Customer != b.Customer:
			return a.Customer.String() < b.Customer.String()
		case a.Meter != b.Meter:
			return a.Meter < b.Meter
		}
		return a.Generation < b.Generation
	})
	return keys
}

// SortedHours is SortedOnTime for hour rows.
func (d *Delta) SortedHours() []HourKey {
	keys := make([]HourKey, 0, len(d.Hours))
	for k := range d.Hours {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		switch {
		case a.Tenant != b.Tenant:
			return a.Tenant.String() < b.Tenant.String()
		case a.Customer != b.Customer:
			return a.Customer.String() < b.Customer.String()
		case a.Meter != b.Meter:
			return a.Meter < b.Meter
		case a.Hour != b.Hour:
			return a.Hour < b.Hour
		}
		return a.Generation < b.Generation
	})
	return keys
}

// SortedLate is SortedOnTime for late rows.
func (d *Delta) SortedLate() []LateKey {
	keys := make([]LateKey, 0, len(d.Late))
	for k := range d.Late {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		switch {
		case a.Tenant != b.Tenant:
			return a.Tenant.String() < b.Tenant.String()
		case a.Billed != b.Billed:
			return a.Billed < b.Billed
		case a.Customer != b.Customer:
			return a.Customer.String() < b.Customer.String()
		case a.Meter != b.Meter:
			return a.Meter < b.Meter
		case a.Origin != b.Origin:
			return a.Origin < b.Origin
		}
		return a.Generation < b.Generation
	})
	return keys
}
