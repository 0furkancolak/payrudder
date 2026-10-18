package outbox

import (
	"context"
	"errors"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/0furkancolak/quotify/pkg/rabbitx"
)

// DestinationHeader selects the sink of a row in a Router. Absent means Kafka.
const (
	DestinationHeader = "x-destination"
	DestKafka         = "kafka"
	DestRabbitMQ      = "rabbitmq"
	// MessageIDHeader becomes the AMQP message id (the dedup key of the consumer).
	MessageIDHeader = "message_id"
)

// AMQPSink publishes rows to RabbitMQ with publisher confirms. Row.Topic is the exchange and Row.PartitionKey the
// routing key; headers become AMQP headers, except message_id which becomes the message id.
type AMQPSink struct{ Publisher *rabbitx.Publisher }

// Publish implements Sink.
func (s AMQPSink) Publish(ctx context.Context, rows []Row) error {
	msgs := make([]rabbitx.Message, len(rows))
	for i, r := range rows {
		hdr := amqp.Table{}
		for k, v := range r.Headers {
			if k == MessageIDHeader || k == DestinationHeader {
				continue
			}
			hdr[k] = v
		}
		msgs[i] = rabbitx.Message{
			Exchange: r.Topic, RoutingKey: r.PartitionKey,
			Publishing: amqp.Publishing{ContentType: "application/x-protobuf", Body: r.Payload, Headers: hdr, MessageId: r.Headers[MessageIDHeader]},
		}
	}
	return s.Publisher.Publish(ctx, msgs...)
}

// Router sends each row to the sink its x-destination header names, so one relay drains an outbox that feeds both
// Kafka and RabbitMQ. Order is preserved per destination.
type Router struct {
	Kafka Sink
	AMQP  Sink
}

// Publish implements Sink.
func (r Router) Publish(ctx context.Context, rows []Row) error {
	var kafka, rabbit []Row
	for _, row := range rows {
		if row.Headers[DestinationHeader] == DestRabbitMQ {
			rabbit = append(rabbit, row)
		} else {
			kafka = append(kafka, row)
		}
	}
	if (len(rabbit) > 0 && r.AMQP == nil) || (len(kafka) > 0 && r.Kafka == nil) {
		return errors.New("outbox: a row names a destination this relay has no sink for")
	}
	if len(rabbit) > 0 {
		if err := r.AMQP.Publish(ctx, rabbit); err != nil {
			return err
		}
	}
	if len(kafka) > 0 {
		if err := r.Kafka.Publish(ctx, kafka); err != nil {
			return err
		}
	}
	return nil
}
