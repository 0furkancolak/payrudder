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
	// Value is the total to add for SUM and COUNT; the maximum seen for MAX; the quantity of the latest event, which is Order, for LAST; and, for
	// UNIQUE, 0 until the distinct step of the batch adds the number of values that were new.
	Value       *big.Int
	Scale       int
	Events      int64
	Aggregation usage.Aggregation
	// Order is the position of the event Value is of (LAST only).
	Order usage.Order
}

// Mode names how the upsert merges a cell into its stored row: "sum" adds (SUM, COUNT and the new-value counts of UNIQUE), "max" keeps the greater
// and "last" keeps the value of the later event.
func Mode(a usage.Aggregation) string {
	switch a {
	case usage.Max:
		return "max"
	case usage.Last:
		return "last"
	}
	return "sum"
}

// Delta collects the pre-summed contributions of one batch (one cell per distinct row, whatever the number of events).
type Delta struct {
	OnTime map[OnTimeKey]*Cell
	// Hours are the hour totals of the on-time events, which limits-svc reconciles its short quota windows against.
	Hours map[HourKey]*Cell
	Late  map[LateKey]*Cell
	// Seen are the distinct values of the batch's UNIQUE events, per period: "" for a value some on-time event carried, else the earliest period the
	// value was billed in as late usage (see MergeSeen).
	Seen map[SeenKey]string
	// HourSeen are the distinct values of the on-time UNIQUE events per hour.
	HourSeen map[HourSeenKey]struct{}
}

// NewDelta returns an empty Delta.
func NewDelta() *Delta {
	return &Delta{OnTime: map[OnTimeKey]*Cell{}, Hours: map[HourKey]*Cell{}, Late: map[LateKey]*Cell{}, Seen: map[SeenKey]string{}, HourSeen: map[HourSeenKey]struct{}{}}
}

func (c *Cell) add(m usage.Meter, e usage.Event) {
	v := m.Value(e)
	if c.Value == nil {
		c.Value, c.Scale, c.Aggregation, c.Events = big.NewInt(v), e.Scale, m.Aggregation, 1
		if m.Aggregation == usage.Unique {
			c.Scale = 0 // a count of values
		}
		if m.Aggregation == usage.Last {
			c.Order = usage.OrderOf(e)
		}
		return
	}
	c.Events++
	switch m.Aggregation {
	case usage.Max:
		if n := big.NewInt(v); n.Cmp(c.Value) > 0 {
			c.Value = n
		}
	case usage.Last:
		if o := usage.OrderOf(e); o.After(c.Order) {
			c.Value, c.Order = big.NewInt(v), o
		}
	default:
		c.Value.Add(c.Value, big.NewInt(v))
	}
}

// Merge combines two cells of the same row, the way the upsert combines a stored row with a cell. It is associative, commutative and, for MAX and
// LAST, idempotent; the tests state those laws, and the SQL of the upsert is held to this function.
func Merge(a, b *Cell) *Cell {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	out := &Cell{Value: new(big.Int).Set(a.Value), Scale: max(a.Scale, b.Scale), Events: a.Events + b.Events, Aggregation: a.Aggregation, Order: a.Order}
	switch a.Aggregation {
	case usage.Max:
		if b.Value.Cmp(out.Value) > 0 {
			out.Value.Set(b.Value)
		}
	case usage.Last:
		if b.Order.After(a.Order) {
			out.Value.Set(b.Value)
			out.Order = b.Order
		}
	default:
		out.Value.Add(out.Value, b.Value)
	}
	return out
}

// AddOnTime merges an on-time event into the cell of its row.
func (d *Delta) AddOnTime(k OnTimeKey, m usage.Meter, e usage.Event) {
	c := d.OnTime[k]
	if c == nil {
		c = &Cell{}
		d.OnTime[k] = c
	}
	c.add(m, e)
}

// AddHour merges an on-time event into the cell of its hour row.
func (d *Delta) AddHour(k HourKey, m usage.Meter, e usage.Event) {
	c := d.Hours[k]
	if c == nil {
		c = &Cell{}
		d.Hours[k] = c
	}
	c.add(m, e)
}

// AddLate merges a late event into the cell of its row.
func (d *Delta) AddLate(k LateKey, m usage.Meter, e usage.Event) {
	c := d.Late[k]
	if c == nil {
		c = &Cell{}
		d.Late[k] = c
	}
	c.add(m, e)
}

// AddDistinctOnTime moves the distinct count of an on-time row by n (negative when a value that was billed as late usage turns out to be on time).
// The cell is created, with no event, for a row the batch has no event of.
func (d *Delta) AddDistinctOnTime(k OnTimeKey, n int64) {
	c := d.OnTime[k]
	if c == nil {
		c = &Cell{Value: new(big.Int), Aggregation: usage.Unique}
		d.OnTime[k] = c
	}
	c.Value.Add(c.Value, big.NewInt(n))
}

// AddDistinctLate is AddDistinctOnTime for a late usage row.
func (d *Delta) AddDistinctLate(k LateKey, n int64) {
	c := d.Late[k]
	if c == nil {
		c = &Cell{Value: new(big.Int), Aggregation: usage.Unique}
		d.Late[k] = c
	}
	c.Value.Add(c.Value, big.NewInt(n))
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
