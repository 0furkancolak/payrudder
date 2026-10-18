// Package domain holds the aggregator's pure rules: decoding and validating events, meter filters and the merge of
// events into aggregate cells. No I/O.
package domain

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/quotify/gen/go/quotify/events/v1"
)

// Event is a validated usage event.
type Event struct {
	EventID    string
	TenantID   uuid.UUID
	CustomerID uuid.UUID
	Meter      string
	Units      int64
	Scale      int
	EventTime  time.Time
	IngestedAt time.Time
	Period     string
	Props      map[string]string
}

var (
	periodRe  = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)
	eventIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// DecodeError marks a record that can never be applied, however often it's retried. It goes to the DLQ.
type DecodeError struct {
	Reason string
	Err    error
}

func (e *DecodeError) Error() string { return e.Reason + ": " + e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

// Decode parses and validates a usage.events.v1 record.
func Decode(rec *kgo.Record) (Event, error) {
	var pb eventsv1.UsageEvent
	if err := proto.Unmarshal(rec.Value, &pb); err != nil {
		return Event{}, &DecodeError{"DECODE_ERROR", err}
	}
	bad := func(format string, a ...any) (Event, error) {
		return Event{}, &DecodeError{"INVALID_EVENT", fmt.Errorf(format, a...)}
	}
	tenant, err := uuid.Parse(pb.GetTenantId())
	if err != nil {
		return bad("tenant_id %q is not a UUID", pb.GetTenantId())
	}
	customer, err := uuid.Parse(pb.GetCustomerId())
	if err != nil {
		return bad("customer_id %q is not a UUID", pb.GetCustomerId())
	}
	switch {
	case !eventIDRe.MatchString(pb.GetEventId()):
		return bad("event_id %q is malformed", pb.GetEventId())
	case pb.GetMeterCode() == "":
		return bad("meter_code is empty")
	case !periodRe.MatchString(pb.GetPeriodId()):
		return bad("period_id %q is malformed", pb.GetPeriodId())
	case pb.GetQuantityScale() > 6:
		return bad("quantity_scale %d is out of range", pb.GetQuantityScale())
	case pb.GetQuantityUnits() < 0:
		return bad("quantity_units %d is negative", pb.GetQuantityUnits())
	case pb.GetEventTime() == nil || pb.GetEventTime().CheckValid() != nil:
		return bad("event_time is missing or invalid")
	case pb.GetIngestedAt() == nil || pb.GetIngestedAt().CheckValid() != nil:
		return bad("ingested_at is missing or invalid")
	}
	return Event{
		EventID: pb.GetEventId(), TenantID: tenant, CustomerID: customer, Meter: pb.GetMeterCode(),
		Units: pb.GetQuantityUnits(), Scale: int(pb.GetQuantityScale()),
		EventTime: pb.GetEventTime().AsTime(), IngestedAt: pb.GetIngestedAt().AsTime(),
		Period: pb.GetPeriodId(), Props: pb.GetProperties(),
	}, nil
}

// Aggregation is how a meter combines event quantities.
type Aggregation int

const (
	Sum Aggregation = iota
	Count
	Max
)

// Meter is the part of a meter definition the aggregator needs.
type Meter struct {
	Code        string
	Aggregation Aggregation
	// Filter maps a property name to its accepted values. An event matches when every listed property has an accepted value.
	Filter map[string][]string
}

// Matches reports whether an event's properties satisfy the meter filter.
func (m Meter) Matches(props map[string]string) bool {
	for name, accepted := range m.Filter {
		v, ok := props[name]
		if !ok {
			return false
		}
		found := false
		for _, a := range accepted {
			if a == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Value is what an event contributes to its meter's aggregate: its quantity, or 1 for COUNT.
func (m Meter) Value(e Event) int64 {
	if m.Aggregation == Count {
		return 1
	}
	return e.Units
}

// OnTimeKey identifies a usage_aggregates row.
type OnTimeKey struct {
	Tenant, Customer uuid.UUID
	Meter, Period    string
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
	Aggregation Aggregation
}

// Delta collects the pre-summed contributions of one batch (one cell per distinct row, whatever the number of events).
type Delta struct {
	OnTime map[OnTimeKey]*Cell
	Late   map[LateKey]*Cell
}

// NewDelta returns an empty Delta.
func NewDelta() *Delta {
	return &Delta{OnTime: map[OnTimeKey]*Cell{}, Late: map[LateKey]*Cell{}}
}

func (c *Cell) add(v int64, scale int, agg Aggregation) {
	if c.Value == nil {
		c.Value, c.Scale, c.Aggregation = big.NewInt(v), scale, agg
		c.Events = 1
		return
	}
	c.Events++
	if agg == Max {
		if n := big.NewInt(v); n.Cmp(c.Value) > 0 {
			c.Value = n
		}
		return
	}
	c.Value.Add(c.Value, big.NewInt(v))
}

// AddOnTime merges an on-time event into the cell of its row.
func (d *Delta) AddOnTime(k OnTimeKey, m Meter, e Event) {
	c := d.OnTime[k]
	if c == nil {
		c = &Cell{}
		d.OnTime[k] = c
	}
	c.add(m.Value(e), e.Scale, m.Aggregation)
}

// AddLate merges a late event into the cell of its row.
func (d *Delta) AddLate(k LateKey, m Meter, e Event) {
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
