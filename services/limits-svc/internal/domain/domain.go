// Package domain holds limits-svc's pure rules: decoding usage events, meter filters and threshold arithmetic.
// It mirrors the aggregator's rules on purpose: both must count the same events for reconciliation to converge.
package domain

import (
	"fmt"
	"math/big"
	"regexp"
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
	IngestedAt time.Time
	Period     string
	Props      map[string]string
}

var (
	periodRe  = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)
	eventIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// Decode parses and validates a usage.events.v1 record. An error means the record can never be applied.
func Decode(rec *kgo.Record) (Event, error) {
	var pb eventsv1.UsageEvent
	if err := proto.Unmarshal(rec.Value, &pb); err != nil {
		return Event{}, fmt.Errorf("decode: %w", err)
	}
	tenant, err := uuid.Parse(pb.GetTenantId())
	if err != nil {
		return Event{}, fmt.Errorf("tenant_id %q is not a UUID", pb.GetTenantId())
	}
	customer, err := uuid.Parse(pb.GetCustomerId())
	if err != nil {
		return Event{}, fmt.Errorf("customer_id %q is not a UUID", pb.GetCustomerId())
	}
	switch {
	case !eventIDRe.MatchString(pb.GetEventId()):
		return Event{}, fmt.Errorf("event_id %q is malformed", pb.GetEventId())
	case pb.GetMeterCode() == "":
		return Event{}, fmt.Errorf("meter_code is empty")
	case !periodRe.MatchString(pb.GetPeriodId()):
		return Event{}, fmt.Errorf("period_id %q is malformed", pb.GetPeriodId())
	case pb.GetQuantityScale() > 6:
		return Event{}, fmt.Errorf("quantity_scale %d is out of range", pb.GetQuantityScale())
	case pb.GetQuantityUnits() < 0:
		return Event{}, fmt.Errorf("quantity_units %d is negative", pb.GetQuantityUnits())
	case pb.GetIngestedAt() == nil || pb.GetIngestedAt().CheckValid() != nil:
		return Event{}, fmt.Errorf("ingested_at is missing or invalid")
	}
	return Event{
		EventID: pb.GetEventId(), TenantID: tenant, CustomerID: customer, Meter: pb.GetMeterCode(),
		Units: pb.GetQuantityUnits(), Scale: int(pb.GetQuantityScale()),
		IngestedAt: pb.GetIngestedAt().AsTime(), Period: pb.GetPeriodId(), Props: pb.GetProperties(),
	}, nil
}

// Aggregation is how a meter combines event quantities.
type Aggregation int

const (
	Sum Aggregation = iota
	Count
	Max
)

// Meter is the part of a meter definition limits-svc needs.
type Meter struct {
	Code        string
	Aggregation Aggregation
	Scale       int
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

// Value is what an event contributes to its meter's counter: its quantity, or 1 for COUNT.
func (m Meter) Value(e Event) int64 {
	if m.Aggregation == Count {
		return 1
	}
	return e.Units
}

// MaxCounter is the largest value a counter may reach: Lua compares numbers as doubles, exact up to 2^53.
const MaxCounter = int64(1) << 53

// Crossed reports whether before < pct% of limit <= after, in exact integer arithmetic.
func Crossed(before, after, limit int64, pct int32) bool {
	if limit <= 0 || pct <= 0 {
		return false
	}
	t := new(big.Int).Mul(big.NewInt(limit), big.NewInt(int64(pct)))
	return times100(before).Cmp(t) < 0 && t.Cmp(times100(after)) <= 0
}

// Reached reports whether used has reached pct% of limit.
func Reached(used, limit int64, pct int32) bool {
	if limit <= 0 || pct <= 0 {
		return false
	}
	t := new(big.Int).Mul(big.NewInt(limit), big.NewInt(int64(pct)))
	return times100(used).Cmp(t) >= 0
}

func times100(v int64) *big.Int { return new(big.Int).Mul(big.NewInt(v), big.NewInt(100)) }

// eventNamespace scopes the uuidv5 ids of LimitExceeded events.
var eventNamespace = uuid.MustParse("5b0e6c52-8f0f-5a43-a3a1-3f1d6c6e0a11")

// LimitEventID is the deterministic id of the LimitExceeded event of one threshold in one period.
func LimitEventID(tenant, customer uuid.UUID, meter, period string, pct int32) string {
	return uuid.NewSHA1(eventNamespace, fmt.Appendf(nil, "%s|%s|%s|%s|%d", tenant, customer, meter, period, pct)).String()
}
