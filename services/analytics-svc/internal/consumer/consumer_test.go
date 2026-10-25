package consumer

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
)

func usageRecord(t *testing.T, edit func(*eventsv1.UsageEvent)) *kgo.Record {
	t.Helper()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	pb := &eventsv1.UsageEvent{EventId: "ev1", TenantId: uuid.NewString(), CustomerId: uuid.NewString(), CustomerExternalId: "cus", MeterCode: "m",
		PeriodId: "2026-11", QuantityUnits: 3, QuantityScale: 2, EventTime: timestamppb.New(now), IngestedAt: timestamppb.New(now)}
	edit(pb)
	b, err := proto.Marshal(pb)
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{Topic: UsageTopic, Value: b}
}

// TestDecodeUsageFollowsTheSharedRule: analytics used to accept events the aggregator dead-letters (no event id format,
// no period check, negative units). It now decodes through pkg/usage, so reporting never shows an event billing refused.
func TestDecodeUsageFollowsTheSharedRule(t *testing.T) {
	row, err := decodeUsage(usageRecord(t, func(*eventsv1.UsageEvent) {}))
	if err != nil {
		t.Fatal(err)
	}
	if row.EventID != "ev1" || row.ExternalID != "cus" || row.Units != 3 || row.Scale != 2 || row.Properties == nil {
		t.Fatalf("%+v", row)
	}
	for name, edit := range map[string]func(*eventsv1.UsageEvent){
		"event id with a space": func(e *eventsv1.UsageEvent) { e.EventId = "a b" },
		"no period":             func(e *eventsv1.UsageEvent) { e.PeriodId = "" },
		"period month 13":       func(e *eventsv1.UsageEvent) { e.PeriodId = "2026-13" },
		"negative units":        func(e *eventsv1.UsageEvent) { e.QuantityUnits = -1 },
		"no event time":         func(e *eventsv1.UsageEvent) { e.EventTime = nil },
	} {
		_, err := decodeUsage(usageRecord(t, edit))
		var de *decodeError
		if !errors.As(err, &de) || de.reason != "INVALID_EVENT" {
			t.Errorf("%s: err = %v, want a decodeError INVALID_EVENT", name, err)
		}
	}
}
