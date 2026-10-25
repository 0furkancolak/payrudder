package kafkax

import (
	"context"
	"math/rand/v2"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/payrudder/pkg/otelx"
)

// DebugHeader marks a record whose trace was asked for explicitly (sandbox tenants and operators may send
// `x-payrudder-debug: 1`): every consumer gives it its own span, instead of the usual 1 % sample.
const DebugHeader = "x-payrudder-debug"

// RecordSampleRate is the share of records that get a span of their own, child of the producer's span.
const RecordSampleRate = 0.01

// maxLinks bounds the span links of one batch span.
const maxLinks = 128

const tracerName = "github.com/0furkancolak/payrudder/pkg/kafkax"

// ProducerContext extracts the trace context a producer stored in the record's headers.
func ProducerContext(rec *kgo.Record) trace.SpanContext {
	ctx := otel.GetTextMapPropagator().Extract(context.Background(), otelx.KafkaHeadersCarrier{Record: rec})
	return trace.SpanContextFromContext(ctx)
}

func isDebug(rec *kgo.Record) bool {
	for _, h := range rec.Headers {
		if h.Key == DebugHeader && string(h.Value) == "1" {
			return true
		}
	}
	return false
}

// TenantOf returns the tenant id of a record keyed by a tenant id (usage events, limit alerts), or "".
func TenantOf(rec *kgo.Record) string {
	if _, err := uuid.Parse(string(rec.Key)); err == nil {
		return string(rec.Key)
	}
	return ""
}

// StartBatch starts the consumer span of a batch of records of one partition ("<topic> process"). Because a batch has
// many producers, the span carries a link to each record's producer context (up to 128) instead of one parent. Records
// that are debug-marked, and 1 % of the others, also get a span of their own whose parent is the producer's span, so
// that their end-to-end trace is one trace (docs/08-observability.md#propagation). The returned function ends every span
// it started.
func StartBatch(ctx context.Context, topic, group string, partition int32, recs []*kgo.Record) (context.Context, func(err error)) {
	tracer := otel.Tracer(tracerName)
	var links []trace.Link
	seen := map[trace.SpanID]bool{}
	for _, r := range recs {
		sc := ProducerContext(r)
		if !sc.IsValid() || seen[sc.SpanID()] {
			continue
		}
		seen[sc.SpanID()] = true
		if len(links) < maxLinks {
			links = append(links, trace.Link{SpanContext: sc})
		}
	}
	base := []attribute.KeyValue{
		attribute.String("messaging.system", "kafka"),
		attribute.String("messaging.destination.name", topic),
		attribute.String("messaging.consumer.group.name", group),
		attribute.Int("messaging.destination.partition.id", int(partition)),
		attribute.String("messaging.operation.type", "process"),
	}
	bctx, bspan := tracer.Start(ctx, topic+" process", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithLinks(links...),
		trace.WithAttributes(append(base, attribute.Int("messaging.batch.message_count", len(recs)),
			attribute.Int64("messaging.kafka.offset.first", recs[0].Offset), attribute.Int64("messaging.kafka.offset.last", recs[len(recs)-1].Offset))...))

	var recordSpans []trace.Span
	for _, r := range recs {
		if sc := ProducerContext(r); sc.IsValid() && (isDebug(r) || rand.Float64() < RecordSampleRate) { //nolint:gosec // sampling, not security
			pctx := trace.ContextWithRemoteSpanContext(context.Background(), sc)
			attrs := append([]attribute.KeyValue(nil), base...)
			attrs = append(attrs, attribute.Int64("messaging.kafka.offset", r.Offset), attribute.String("payrudder.record_key", string(r.Key)))
			if t := TenantOf(r); t != "" {
				attrs = append(attrs, attribute.String("payrudder.tenant_id", t))
			}
			_, span := tracer.Start(pctx, topic+" process", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(attrs...),
				trace.WithLinks(trace.Link{SpanContext: bspan.SpanContext()}))
			recordSpans = append(recordSpans, span)
		}
	}
	return bctx, func(err error) {
		for _, s := range recordSpans {
			if err != nil {
				s.SetStatus(codes.Error, err.Error())
			}
			s.End()
		}
		if err != nil {
			bspan.RecordError(err)
			bspan.SetStatus(codes.Error, err.Error())
		}
		bspan.End()
	}
}

// StartProduce starts the producer-side span of one record ("<topic> publish") as a child of the context stored in the
// record's headers, and returns a context for the publish call.
func StartProduce(rec *kgo.Record) (context.Context, trace.Span) {
	parent := otel.GetTextMapPropagator().Extract(context.Background(), otelx.KafkaHeadersCarrier{Record: rec})
	attrs := []attribute.KeyValue{
		attribute.String("messaging.system", "kafka"),
		attribute.String("messaging.destination.name", rec.Topic),
		attribute.String("messaging.operation.type", "publish"),
		attribute.String("payrudder.record_key", string(rec.Key)),
	}
	if t := TenantOf(rec); t != "" {
		attrs = append(attrs, attribute.String("payrudder.tenant_id", t))
	}
	return otel.Tracer(tracerName).Start(parent, rec.Topic+" publish", trace.WithSpanKind(trace.SpanKindProducer), trace.WithAttributes(attrs...))
}

// ShouldTraceRecord reports whether a record gets its own span (debug-marked, or the 1 % sample).
func ShouldTraceRecord(rec *kgo.Record) bool {
	return isDebug(rec) || rand.Float64() < RecordSampleRate //nolint:gosec // sampling, not security
}
