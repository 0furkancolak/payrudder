package rabbitx

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/payrudder/pkg/otelx"
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

// RequeueBaseDelay is how long a Requeue waits before the message goes back to the queue; every redelivery doubles it (the broker's x-delivery-count). The
// quorum queue dead-letters a message after DeliveryLimit redeliveries, so an immediate requeue would spend them in milliseconds on an outage of a second.
var RequeueBaseDelay = time.Second

// ShutdownGrace is how long a handler that is still running when the consumer is stopped may take to finish and be acked.
var ShutdownGrace = 30 * time.Second

// requeueDelay is the wait before d goes back to the queue: 1, 2, 4 and then 8 times RequeueBaseDelay.
func requeueDelay(d amqp.Delivery) time.Duration {
	var n int64
	switch v := d.Headers["x-delivery-count"].(type) {
	case int64:
		n = v
	case int32:
		n = int64(v)
	case int:
		n = int64(v)
	}
	return RequeueBaseDelay << min(max(n, 0), 3)
}

// requeue puts d back on the queue after its delay, or at once when stop (the consumer's context) ends.
func requeue(stop context.Context, d amqp.Delivery) {
	t := time.NewTimer(requeueDelay(d))
	defer t.Stop()
	select {
	case <-t.C:
	case <-stop.Done():
	}
	_ = d.Nack(false, true)
}

// Consume reads queue until ctx is done, reconnecting on connection loss. Deliveries are handled one at a time per
// goroutine, up to prefetch in flight, and acked only after the handler returns. When ctx is done the handlers still running
// are waited for (up to ShutdownGrace) and their acks sent before Consume returns; their context is not cancelled by ctx.
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
	// Handlers outlive ctx: it only stops new deliveries from being taken. They are waited for before the connection closes (this defer runs first), so
	// their acks and retry publishes still reach the broker; a handler that overstays the grace is cancelled.
	hctx, hcancel := context.WithCancel(context.WithoutCancel(ctx))
	defer hcancel()
	var inflight sync.WaitGroup
	defer inflight.Wait()
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-time.After(ShutdownGrace):
				hcancel()
			case <-hctx.Done():
			}
		case <-hctx.Done():
		}
	}()
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
			hcancel() // the acks of the running handlers have nowhere to go
			if e == nil {
				return errors.New("connection closed")
			}
			return e
		case d, ok := <-deliveries:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				hcancel()
				return errors.New("delivery channel closed")
			}
			sem <- struct{}{}
			inflight.Add(1)
			go func() {
				defer inflight.Done()
				defer func() { <-sem }()
				settle(hctx, ctx, d, queue, h, retryPub, log)
			}()
		}
	}
}

// consumerSpan starts the "<queue> process" span of a delivery as a child of the context the publisher stored in the
// message headers, so a command's processing and its reply continue the trace that issued the command.
func consumerSpan(ctx context.Context, d amqp.Delivery, queue string) (context.Context, trace.Span) {
	parent := otel.GetTextMapPropagator().Extract(ctx, otelx.AMQPTableCarrier(d.Headers))
	return otel.Tracer("github.com/0furkancolak/payrudder/pkg/rabbitx").Start(parent, queue+" process",
		trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", queue),
			attribute.String("messaging.operation.type", "process"),
			attribute.String("messaging.message.id", d.MessageId),
			attribute.Int("payrudder.attempt", Attempt(d)),
		))
}

// settle runs h on d and answers the broker. ctx is the handler's; stop is the consumer's, which only cuts a requeue wait short.
func settle(ctx, stop context.Context, d amqp.Delivery, queue string, h Handler, retryPub *Publisher, log *slog.Logger) {
	ctx, span := consumerSpan(ctx, d, queue)
	defer span.End()
	defer func() {
		if r := recover(); r != nil {
			// Requeue rather than dead-letter: the quorum queue's delivery limit (5) dead-letters a command that keeps
			// crashing its handler, which is how a crash loop ends.
			log.Error("handler panic, requeueing the delivery", "queue", queue, "panic", r)
			requeue(stop, d)
		}
	}()
	outcome := h(ctx, d)
	span.SetAttributes(attribute.Int("payrudder.outcome", int(outcome)))
	switch outcome {
	case Ack:
		_ = d.Ack(false)
	case Requeue:
		requeue(stop, d)
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
