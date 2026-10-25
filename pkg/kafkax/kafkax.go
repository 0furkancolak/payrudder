// Package kafkax builds Kafka producers with Payrudder's settings (docs/04-messaging.md) and carries the required
// record headers: content-type, ce-type, ce-id, tenant-id and the W3C trace context.
package kafkax

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
)

// Header names every record on a domain topic carries.
const (
	HeaderContentType = "content-type"
	HeaderCEType      = "ce-type"
	HeaderCEID        = "ce-id"
	HeaderTenantID    = "tenant-id"
	HeaderTraceparent = "traceparent"
	HeaderTracestate  = "tracestate"

	ContentTypeProtobuf = "application/x-protobuf"
)

// NewProducer returns a client with an idempotent producer: acks=all, zstd, 5 ms linger. franz-go enables
// idempotence by default, and it requires acks=all, which is set explicitly here for clarity.
func NewProducer(brokers []string, extra ...kgo.Opt) (*kgo.Client, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.ZstdCompression()),
		kgo.ProducerLinger(5 * time.Millisecond),
		kgo.RecordDeliveryTimeout(30 * time.Second),
		kgo.ProduceRequestTimeout(10 * time.Second),
	}
	cl, err := kgo.NewClient(append(opts, extra...)...)
	if err != nil {
		return nil, fmt.Errorf("kafkax: new producer: %w", err)
	}
	return cl, nil
}

// Headers converts a map to record headers, sorted by key so that records are byte-for-byte reproducible.
func Headers(m map[string]string) []kgo.RecordHeader {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kgo.RecordHeader, 0, len(keys))
	for _, k := range keys {
		out = append(out, kgo.RecordHeader{Key: k, Value: []byte(m[k])})
	}
	return out
}

// HeaderMap is the inverse of Headers (the last value of a repeated key wins).
func HeaderMap(hs []kgo.RecordHeader) map[string]string {
	out := make(map[string]string, len(hs))
	for _, h := range hs {
		out[h.Key] = string(h.Value)
	}
	return out
}

// Standard returns the required headers of one event.
func Standard(ceType, eventID, tenantID string) map[string]string {
	return map[string]string{
		HeaderContentType: ContentTypeProtobuf,
		HeaderCEType:      ceType,
		HeaderCEID:        eventID,
		HeaderTenantID:    tenantID,
	}
}

// InjectTrace adds the W3C trace context of ctx (traceparent, tracestate) to m using the global propagator.
func InjectTrace(ctx context.Context, m map[string]string, p propagation.TextMapPropagator) {
	p.Inject(ctx, propagation.MapCarrier(m))
}
