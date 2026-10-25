package usage

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func valid() *eventsv1.UsageEvent {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	return &eventsv1.UsageEvent{
		EventId: "ev_1-A", TenantId: uuid.NewString(), CustomerId: uuid.NewString(), CustomerExternalId: "cus_1", MeterCode: "api_calls",
		PeriodId: "2026-11", QuantityUnits: 5, QuantityScale: 3, EventTime: timestamppb.New(now), IngestedAt: timestamppb.New(now.Add(time.Second)),
		Properties: map[string]string{"region": "eu"},
	}
}

func rec(t *testing.T, pb *eventsv1.UsageEvent) *kgo.Record {
	t.Helper()
	b, err := proto.Marshal(pb)
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{Value: b}
}

// TestDecode is the table every consumer of usage.events.v1 shares: a record is valid for all of them or for none.
func TestDecode(t *testing.T) {
	ev, err := Decode(rec(t, valid()))
	if err != nil {
		t.Fatal(err)
	}
	if ev.EventID != "ev_1-A" || ev.Meter != "api_calls" || ev.Units != 5 || ev.Scale != 3 || ev.Period != "2026-11" ||
		ev.CustomerExternalID != "cus_1" || ev.Props["region"] != "eu" || ev.EventTime.IsZero() || ev.IngestedAt.IsZero() {
		t.Fatalf("decoded wrongly: %+v", ev)
	}

	cases := []struct {
		name string
		edit func(*eventsv1.UsageEvent)
	}{
		{"tenant not a uuid", func(e *eventsv1.UsageEvent) { e.TenantId = "x" }},
		{"customer not a uuid", func(e *eventsv1.UsageEvent) { e.CustomerId = "" }},
		{"empty event id", func(e *eventsv1.UsageEvent) { e.EventId = "" }},
		{"event id with a space", func(e *eventsv1.UsageEvent) { e.EventId = "a b" }},
		{"event id too long", func(e *eventsv1.UsageEvent) { e.EventId = string(make([]byte, 65)) }},
		{"empty meter", func(e *eventsv1.UsageEvent) { e.MeterCode = "" }},
		{"period without a month", func(e *eventsv1.UsageEvent) { e.PeriodId = "2026" }},
		{"period month 13", func(e *eventsv1.UsageEvent) { e.PeriodId = "2026-13" }},
		{"empty period", func(e *eventsv1.UsageEvent) { e.PeriodId = "" }},
		{"scale 7", func(e *eventsv1.UsageEvent) { e.QuantityScale = 7 }},
		{"negative units", func(e *eventsv1.UsageEvent) { e.QuantityUnits = -1 }},
		{"missing event_time", func(e *eventsv1.UsageEvent) { e.EventTime = nil }},
		{"invalid event_time", func(e *eventsv1.UsageEvent) { e.EventTime = &timestamppb.Timestamp{Seconds: 1 << 60} }},
		{"missing ingested_at", func(e *eventsv1.UsageEvent) { e.IngestedAt = nil }},
		{"invalid ingested_at", func(e *eventsv1.UsageEvent) { e.IngestedAt = &timestamppb.Timestamp{Seconds: -1 << 60} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pb := valid()
			c.edit(pb)
			_, err := Decode(rec(t, pb))
			var de *DecodeError
			if !errors.As(err, &de) || de.Reason != ReasonInvalid {
				t.Fatalf("err = %v, want INVALID_EVENT", err)
			}
		})
	}

	_, err = Decode(&kgo.Record{Value: []byte{0xff, 0xff, 0xff}})
	var de *DecodeError
	if !errors.As(err, &de) || de.Reason != ReasonDecode {
		t.Fatalf("garbage: err = %v, want DECODE_ERROR", err)
	}
}

func TestMeterMatchesAndValue(t *testing.T) {
	m := Meter{Aggregation: Sum, Filter: map[string][]string{"region": {"eu", "us"}, "tier": {"pro"}}}
	for _, c := range []struct {
		props map[string]string
		want  bool
	}{
		{map[string]string{"region": "eu", "tier": "pro"}, true},
		{map[string]string{"region": "us", "tier": "pro", "extra": "x"}, true},
		{map[string]string{"region": "ap", "tier": "pro"}, false},
		{map[string]string{"region": "eu"}, false},
		{nil, false},
	} {
		if got := m.Matches(c.props); got != c.want {
			t.Errorf("Matches(%v) = %v, want %v", c.props, got, c.want)
		}
	}
	if !(Meter{}).Matches(nil) {
		t.Error("a meter without a filter must match everything")
	}
	e := Event{Units: 42}
	if (Meter{Aggregation: Sum}).Value(e) != 42 || (Meter{Aggregation: Max}).Value(e) != 42 || (Meter{Aggregation: Count}).Value(e) != 1 {
		t.Error("Value: SUM and MAX contribute the units, COUNT contributes 1")
	}
}

func TestFromProto(t *testing.T) {
	m := FromProto(&plansv1.Meter{Code: "c", Aggregation: plansv1.Aggregation_AGGREGATION_MAX, QuantityScale: 3,
		Filter: map[string]*plansv1.FilterValues{"region": {Values: []string{"eu"}}}})
	if m.Code != "c" || m.Aggregation != Max || m.Scale != 3 || len(m.Filter["region"]) != 1 {
		t.Fatalf("%+v", m)
	}
	if FromProto(&plansv1.Meter{Aggregation: plansv1.Aggregation_AGGREGATION_COUNT}).Aggregation != Count ||
		FromProto(&plansv1.Meter{}).Aggregation != Sum || FromProto(&plansv1.Meter{}).Filter != nil {
		t.Fatal("unspecified aggregation is SUM, COUNT stays COUNT, no filter is nil")
	}
}
