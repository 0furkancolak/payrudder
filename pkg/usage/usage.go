// Package usage is the one definition of what a usage.events.v1 record is and which meters count it. The aggregator
// (billing), limits-svc (entitlement counters) and analytics-svc (reporting) all decode through Decode, so an event one
// of them rejects is rejected by all three and their totals converge on reconciliation. No I/O.
package usage

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
)

// Reasons of a DecodeError. They are the dead-letter reasons of the consumers.
const (
	ReasonDecode  = "DECODE_ERROR"  // the bytes are not a UsageEvent
	ReasonInvalid = "INVALID_EVENT" // a field breaks the rules below
)

// MaxScale is the largest quantity_scale of an event.
const MaxScale = 6

var eventIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Event is a validated usage event.
type Event struct {
	EventID    string
	TenantID   uuid.UUID
	CustomerID uuid.UUID
	// CustomerExternalID is the customer's id in the tenant's own system; it is not validated.
	CustomerExternalID string
	Meter              string
	Units              int64
	Scale              int
	EventTime          time.Time
	IngestedAt         time.Time
	Period             string
	Props              map[string]string
}

// DecodeError marks a record that can never be applied, however often it is retried. It goes to the DLQ.
type DecodeError struct {
	Reason string
	Err    error
}

func (e *DecodeError) Error() string { return e.Reason + ": " + e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

// Decode parses and validates a usage.events.v1 record. Every error is a *DecodeError. The rules:
// tenant_id and customer_id are UUIDs; event_id is 1 to 64 characters of [A-Za-z0-9_-]; meter_code is set; period_id is
// YYYY-MM; quantity_scale is at most 6; quantity_units is not negative; event_time and ingested_at are valid timestamps.
func Decode(rec *kgo.Record) (Event, error) {
	var pb eventsv1.UsageEvent
	if err := proto.Unmarshal(rec.Value, &pb); err != nil {
		return Event{}, &DecodeError{ReasonDecode, err}
	}
	bad := func(format string, a ...any) (Event, error) {
		return Event{}, &DecodeError{ReasonInvalid, fmt.Errorf(format, a...)}
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
	case !period.Valid(pb.GetPeriodId()):
		return bad("period_id %q is malformed", pb.GetPeriodId())
	case pb.GetQuantityScale() > MaxScale:
		return bad("quantity_scale %d is out of range", pb.GetQuantityScale())
	case pb.GetQuantityUnits() < 0:
		return bad("quantity_units %d is negative", pb.GetQuantityUnits())
	case pb.GetEventTime() == nil || pb.GetEventTime().CheckValid() != nil:
		return bad("event_time is missing or invalid")
	case pb.GetIngestedAt() == nil || pb.GetIngestedAt().CheckValid() != nil:
		return bad("ingested_at is missing or invalid")
	}
	return Event{
		EventID: pb.GetEventId(), TenantID: tenant, CustomerID: customer, CustomerExternalID: pb.GetCustomerExternalId(),
		Meter: pb.GetMeterCode(), Units: pb.GetQuantityUnits(), Scale: int(pb.GetQuantityScale()),
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
	// Unique counts the distinct values of the meter's value_property, whatever the events' quantities are.
	Unique
	// Last is the quantity of the latest event, in the total order of Order.
	Last
)

// Additive reports whether the totals of two disjoint sets of events add up to the total of their union. Only SUM and COUNT do: a maximum is the
// maximum of the parts, the latest value is the latest of the parts, and the distinct values of two parts overlap.
func (a Aggregation) Additive() bool { return a == Sum || a == Count }

// Meter is the part of a meter definition the consumers of usage events need.
type Meter struct {
	Code        string
	Aggregation Aggregation
	// ValueProperty is the event property a UNIQUE meter counts the distinct values of. The other aggregations don't read it (the quantity of
	// an event is its quantity_units).
	ValueProperty string
	// Scale is the meter's quantity scale (limits-svc renders its counters with it).
	Scale int
	// Filter maps a property name to its accepted values. An event matches when every listed property has an accepted value.
	Filter map[string][]string
}

// FromProto converts a plans-svc meter.
func FromProto(m *plansv1.Meter) Meter {
	out := Meter{Code: m.GetCode(), Scale: int(m.GetQuantityScale()), ValueProperty: m.GetValueProperty()}
	switch m.GetAggregation() {
	case plansv1.Aggregation_AGGREGATION_COUNT:
		out.Aggregation = Count
	case plansv1.Aggregation_AGGREGATION_MAX:
		out.Aggregation = Max
	case plansv1.Aggregation_AGGREGATION_UNIQUE:
		out.Aggregation = Unique
	case plansv1.Aggregation_AGGREGATION_LAST:
		out.Aggregation = Last
	default:
		out.Aggregation = Sum
	}
	if len(m.GetFilter()) > 0 {
		out.Filter = make(map[string][]string, len(m.GetFilter()))
		for k, v := range m.GetFilter() {
			out.Filter[k] = v.GetValues()
		}
	}
	return out
}

// Matches reports whether an event's properties satisfy the meter filter.
func (m Meter) Matches(props map[string]string) bool {
	for name, accepted := range m.Filter {
		v, ok := props[name]
		if !ok || !slices.Contains(accepted, v) {
			return false
		}
	}
	return true
}

// Value is what an event contributes to its meter's aggregate: its quantity, 1 for COUNT, and nothing for UNIQUE (it counts the distinct values of a
// property, see DistinctValue).
func (m Meter) Value(e Event) int64 {
	switch m.Aggregation {
	case Count:
		return 1
	case Unique:
		return 0
	}
	return e.Units
}

// DistinctValue is what a UNIQUE meter counts in an event: the property named by the meter's value_property. ok is false when the event does not
// carry it (or carries it empty), and then the event contributes nothing to the meter.
func (m Meter) DistinctValue(e Event) (value string, ok bool) {
	v := e.Props[m.ValueProperty]
	return v, m.ValueProperty != "" && v != ""
}

// Digest is the identity of a distinct value: the SHA-256 of its bytes. The aggregator stores it instead of the value (a property can be 4 KiB and
// a primary key cannot), and two different values collide with a probability that is not a consideration.
func Digest(value string) [sha256.Size]byte { return sha256.Sum256([]byte(value)) }

// Order is the total order of LAST: the event time at the microsecond (what Postgres and the wire keep), then the event id bytewise. The id of an
// event is assigned once at ingest, so two different events never compare equal, and the latest event of a set does not depend on the order in
// which the events, the partitions or a replay deliver them.
type Order struct {
	Micros int64
	ID     string
}

// OrderOf is the position of an event in the LAST order.
func OrderOf(e Event) Order { return Order{Micros: e.EventTime.UnixMicro(), ID: e.EventID} }

// Compare is -1, 0 or 1 like a comparator.
func (o Order) Compare(p Order) int {
	switch {
	case o.Micros < p.Micros:
		return -1
	case o.Micros > p.Micros:
		return 1
	case o.ID < p.ID: // Go compares strings bytewise
		return -1
	case o.ID > p.ID:
		return 1
	}
	return 0
}

// After reports whether o is strictly later than p.
func (o Order) After(p Order) bool { return o.Compare(p) > 0 }
