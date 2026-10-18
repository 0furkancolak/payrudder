package kafkax

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestHeadersAreSortedAndRoundTrip(t *testing.T) {
	m := Standard("quotify.events.v1.UsageEvent", "01JD", "tenant-1")
	hs := Headers(m)
	for i := 1; i < len(hs); i++ {
		if hs[i-1].Key >= hs[i].Key {
			t.Fatalf("headers not sorted: %v", hs)
		}
	}
	back := HeaderMap(hs)
	if len(back) != 4 || back[HeaderCEType] != "quotify.events.v1.UsageEvent" || back[HeaderContentType] != ContentTypeProtobuf ||
		back[HeaderCEID] != "01JD" || back[HeaderTenantID] != "tenant-1" {
		t.Fatalf("round trip = %v", back)
	}
}

func TestInjectTrace(t *testing.T) {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:  trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}, TraceFlags: trace.FlagsSampled,
	})
	m := map[string]string{}
	InjectTrace(trace.ContextWithSpanContext(context.Background(), sc), m, propagation.TraceContext{})
	if got, want := m[HeaderTraceparent], "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01"; got != want {
		t.Fatalf("traceparent = %q, want %q", got, want)
	}
	empty := map[string]string{}
	InjectTrace(context.Background(), empty, propagation.TraceContext{})
	if len(empty) != 0 {
		t.Fatalf("no span context must add no headers, got %v", empty)
	}
}
