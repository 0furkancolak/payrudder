package outbox

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/0furkancolak/quotify/pkg/kafkax"
)

// KafkaSink publishes rows to Kafka with the given (idempotent, acks=all) client.
type KafkaSink struct{ Client *kgo.Client }

// Publish produces the rows in id order and returns when every record is acknowledged.
func (s KafkaSink) Publish(ctx context.Context, rows []Row) error {
	recs := make([]*kgo.Record, len(rows))
	for i, row := range rows {
		recs[i] = &kgo.Record{
			Topic:   row.Topic,
			Key:     []byte(row.PartitionKey),
			Value:   row.Payload,
			Headers: kafkax.Headers(row.Headers),
		}
	}
	if err := s.Client.ProduceSync(ctx, recs...).FirstErr(); err != nil {
		return fmt.Errorf("kafka produce: %w", err)
	}
	return nil
}
