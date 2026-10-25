package otelx_test

import (
	"context"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/payrudder/pkg/otelx"
)

func sampledContext(t *testing.T) (context.Context, trace.SpanContext) {
	t.Helper()
	tid, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled, Remote: true})
	return trace.ContextWithSpanContext(context.Background(), sc), sc
}

func assertRoundTrip(t *testing.T, carrier propagation.TextMapCarrier) {
	t.Helper()
	ctx, want := sampledContext(t)
	p := otelx.Propagator()
	p.Inject(ctx, carrier)

	if got := carrier.Get("traceparent"); got != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("traceparent = %q", got)
	}
	got := trace.SpanContextFromContext(p.Extract(context.Background(), carrier))
	if !got.Equal(want) {
		t.Fatalf("extracted %v, want %v", got, want)
	}
}

func TestKafkaHeadersCarrierRoundTrip(t *testing.T) {
	rec := &kgo.Record{Headers: []kgo.RecordHeader{{Key: "ce-type", Value: []byte("x")}}}
	assertRoundTrip(t, otelx.KafkaHeadersCarrier{Record: rec})
	if len(rec.Headers) != 2 {
		t.Fatalf("headers = %v, want ce-type + traceparent", rec.Headers)
	}
}

func TestKafkaHeadersCarrierSetReplaces(t *testing.T) {
	rec := &kgo.Record{Headers: []kgo.RecordHeader{{Key: "traceparent", Value: []byte("stale")}, {Key: "traceparent", Value: []byte("stale2")}}}
	c := otelx.KafkaHeadersCarrier{Record: rec}
	c.Set("traceparent", "fresh")
	if len(rec.Headers) != 1 || c.Get("traceparent") != "fresh" {
		t.Fatalf("headers = %v", rec.Headers)
	}
}

func TestAMQPTableCarrierRoundTrip(t *testing.T) {
	assertRoundTrip(t, otelx.AMQPTableCarrier(amqp.Table{}))
}

func TestAMQPTableCarrierBytesValue(t *testing.T) {
	c := otelx.AMQPTableCarrier(amqp.Table{"traceparent": []byte("abc"), "n": int32(1)})
	if c.Get("traceparent") != "abc" || c.Get("n") != "" || c.Get("missing") != "" {
		t.Fatal("unexpected Get results")
	}
}
