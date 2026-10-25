// Package usage is the one definition of what a usage.events.v1 record is and which meters count it. The aggregator
// (billing), limits-svc (entitlement counters) and analytics-svc (reporting) all decode through Decode, so an event one
// of them rejects is rejected by all three and their totals converge on reconciliation. No I/O.
package usage

import (
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
)

// Meter is the part of a meter definition the consumers of usage events need.
type Meter struct {
	Code        string
	Aggregation Aggregation
	// Scale is the meter's quantity scale (limits-svc renders its counters with it).
	Scale int
	// Filter maps a property name to its accepted values. An event matches when every listed property has an accepted value.
	Filter map[string][]string
}

// FromProto converts a plans-svc meter.
func FromProto(m *plansv1.Meter) Meter {
	out := Meter{Code: m.GetCode(), Scale: int(m.GetQuantityScale())}
	switch m.GetAggregation() {
	case plansv1.Aggregation_AGGREGATION_COUNT:
		out.Aggregation = Count
	case plansv1.Aggregation_AGGREGATION_MAX:
		out.Aggregation = Max
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

// Value is what an event contributes to its meter's aggregate: its quantity, or 1 for COUNT.
func (m Meter) Value(e Event) int64 {
	if m.Aggregation == Count {
		return 1
	}
	return e.Units
}
