package rabbitx

import (
	"context"
	"errors"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/quotify/pkg/otelx"
)

// Outcome tells the consumer what to do with a delivery once the handler returns.
type Outcome int

const (
	// Ack: the work is done (or deliberately ignored) and its effects are durable.
	Ack Outcome = iota
	// Retry: a transient error. The delivery is re-published to the retry exchange with x-attempt+1 and acked; after
	// MaxAttempts it is dead-lettered instead.
	Retry
	// Requeue: a transient error where the message should go straight back to the queue (used by reply consumers).
	Requeue
	// Reject: poison. The delivery is dead-lettered (nack without requeue) and lands in the queue's .dlq.
	Reject
)

// Handler processes one delivery.
type Handler func(ctx context.Context, d amqp.Delivery) Outcome

// AttemptHeader counts the retries of a command.
const AttemptHeader = "x-attempt"

// Attempt returns the retry count of a delivery.
func Attempt(d amqp.Delivery) int {
	switch v := d.Headers[AttemptHeader].(type) {
	case int32:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// Consume reads queue until ctx is done, reconnecting on connection loss. Deliveries are handled one at a time per
// goroutine, up to prefetch in flight, and acked only after the handler returns.
func Consume(ctx context.Context, url, queue string, prefetch int, h Handler, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	retryPub := NewPublisher(url)
	defer retryPub.Close()
	for ctx.Err() == nil {
		err := consumeOnce(ctx, url, queue, prefetch, h, retryPub, log)
		if ctx.Err() != nil {
			return nil
		}
		log.Warn("rabbitmq consumer stopped, reconnecting", "queue", queue, "error", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
	return nil
}

func consumeOnce(ctx context.Context, url, queue string, prefetch int, h Handler, retryPub *Publisher, log *slog.Logger) error {
	conn, err := amqp.DialConfig(url, amqp.Config{Heartbeat: 10 * time.Second, Locale: "en_US", Dial: amqp.DefaultDial(5 * time.Second)})
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.ConsumeWithContext(ctx, queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	sem := make(chan struct{}, prefetch)
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-closed:
			if e == nil {
				return errors.New("connection closed")
			}
			return e
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("delivery channel closed")
			}
			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				settle(ctx, d, queue, h, retryPub, log)
			}()
		}
	}
}

// consumerSpan starts the "<queue> process" span of a delivery as a child of the context the publisher stored in the
// message headers, so a command's processing and its reply continue the trace that issued the command.
func consumerSpan(ctx context.Context, d amqp.Delivery, queue string) (context.Context, trace.Span) {
	parent := otel.GetTextMapPropagator().Extract(ctx, otelx.AMQPTableCarrier(d.Headers))
	return otel.Tracer("github.com/0furkancolak/quotify/pkg/rabbitx").Start(parent, queue+" process",
		trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", queue),
			attribute.String("messaging.operation.type", "process"),
			attribute.String("messaging.message.id", d.MessageId),
			attribute.Int("quotify.attempt", Attempt(d)),
		))
}

func settle(ctx context.Context, d amqp.Delivery, queue string, h Handler, retryPub *Publisher, log *slog.Logger) {
	ctx, span := consumerSpan(ctx, d, queue)
	defer span.End()
	defer func() {
		if r := recover(); r != nil {
			// Requeue rather than dead-letter: the quorum queue's delivery limit (5) dead-letters a command that keeps
			// crashing its handler, which is how a crash loop ends.
			log.Error("handler panic, requeueing the delivery", "queue", queue, "panic", r)
			_ = d.Nack(false, true)
		}
	}()
	outcome := h(ctx, d)
	span.SetAttributes(attribute.Int("quotify.outcome", int(outcome)))
	switch outcome {
	case Ack:
		_ = d.Ack(false)
	case Requeue:
		_ = d.Nack(false, true)
	case Reject:
		_ = d.Nack(false, false)
	case Retry:
		attempt := Attempt(d)
		if attempt >= MaxAttempts {
			log.Warn("retries exhausted, dead-lettering", "queue", queue, "attempts", attempt)
			_ = d.Nack(false, false)
			return
		}
		hdr := amqp.Table{}
		for k, v := range d.Headers {
			hdr[k] = v
		}
		hdr[AttemptHeader] = int32(attempt + 1) //nolint:gosec // bounded by MaxAttempts
		err := retryPub.Publish(ctx, Message{
			Exchange: ExchangeRetry, RoutingKey: RetryRoutingKey(queue, attempt+1),
			Publishing: amqp.Publishing{ContentType: d.ContentType, Body: d.Body, Headers: hdr, MessageId: d.MessageId, Type: d.Type},
		})
		if err != nil {
			log.Warn("retry publish failed, requeueing the delivery", "queue", queue, "error", err)
			_ = d.Nack(false, true)
			return
		}
		_ = d.Ack(false)
	}
}
