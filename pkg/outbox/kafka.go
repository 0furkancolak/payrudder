package outbox

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/otelx"
)

// KafkaSink publishes rows to Kafka with the given (idempotent, acks=all) client.
type KafkaSink struct{ Client *kgo.Client }

// Publish produces the rows in id order and returns when every record is acknowledged.
func (s KafkaSink) Publish(ctx context.Context, rows []Row) error {
	recs := make([]*kgo.Record, len(rows))
	var spans []trace.Span
	defer func() {
		for _, sp := range spans {
			sp.End()
		}
	}()
	for i, row := range rows {
		recs[i] = &kgo.Record{
			Topic:   row.Topic,
			Key:     []byte(row.PartitionKey),
			Value:   row.Payload,
			Headers: kafkax.Headers(row.Headers),
		}
		// 1 % of records (and every debug-marked one) get a "<topic> publish" span in the originating request's trace; the
		// consumers continue from it.
		if kafkax.ShouldTraceRecord(recs[i]) {
			if _, span := kafkax.StartProduce(recs[i]); span.SpanContext().IsValid() {
				otel.GetTextMapPropagator().Inject(trace.ContextWithSpan(context.Background(), span), otelx.KafkaHeadersCarrier{Record: recs[i]})
				spans = append(spans, span)
			}
		}
	}
	if err := s.Client.ProduceSync(ctx, recs...).FirstErr(); err != nil {
		return fmt.Errorf("kafka produce: %w", err)
	}
	return nil
}
