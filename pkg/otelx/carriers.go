package otelx

import (
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
)

// KafkaHeadersCarrier adapts the headers of a franz-go record to a propagation.TextMapCarrier.
//
//	otelx.Propagator().Inject(ctx, otelx.KafkaHeadersCarrier{Record: rec})
type KafkaHeadersCarrier struct {
	Record *kgo.Record
}

var _ propagation.TextMapCarrier = KafkaHeadersCarrier{}

// Get returns the first header value for key.
func (c KafkaHeadersCarrier) Get(key string) string {
	for _, h := range c.Record.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// Set replaces every header named key with a single value.
func (c KafkaHeadersCarrier) Set(key, value string) {
	kept := c.Record.Headers[:0]
	for _, h := range c.Record.Headers {
		if h.Key != key {
			kept = append(kept, h)
		}
	}
	c.Record.Headers = append(kept, kgo.RecordHeader{Key: key, Value: []byte(value)})
}

// Keys lists header names.
func (c KafkaHeadersCarrier) Keys() []string {
	keys := make([]string, 0, len(c.Record.Headers))
	for _, h := range c.Record.Headers {
		keys = append(keys, h.Key)
	}
	return keys
}

// AMQPTableCarrier adapts AMQP message headers to a propagation.TextMapCarrier.
// Use it on amqp.Publishing.Headers when publishing and amqp.Delivery.Headers when consuming.
type AMQPTableCarrier amqp.Table

var _ propagation.TextMapCarrier = AMQPTableCarrier{}

// Get returns the header as a string. Brokers may hand back string or []byte values.
func (c AMQPTableCarrier) Get(key string) string {
	switch v := c[key].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}

// Set stores the value as a string header. The table must be non-nil.
func (c AMQPTableCarrier) Set(key, value string) { c[key] = value }

// Keys lists header names.
func (c AMQPTableCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
