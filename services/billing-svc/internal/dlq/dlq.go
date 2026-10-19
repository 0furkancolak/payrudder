// Package dlq inspects and redrives the RabbitMQ dead-letter queues ({queue}.dlq, docs/04-messaging.md) for the operator API.
// It opens a connection per call: this is an operator action, not a hot path.
package dlq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/0furkancolak/quotify/pkg/rabbitx"
)

// ErrUnknownQueue is returned for a queue that has no dead-letter queue.
var ErrUnknownQueue = errors.New("dlq: unknown queue")

// Admin talks to the broker.
type Admin struct{ URL string }

// Queues are the queues that have a dead-letter queue.
func Queues() []string {
	return append(append([]string(nil), rabbitx.CommandQueues...), rabbitx.ReplyQueue)
}

func known(q string) bool {
	for _, k := range Queues() {
		if k == q {
			return true
		}
	}
	return false
}

// Depth is a queue and the messages in its dead-letter queue.
type Depth struct {
	Queue    string
	Messages int64
}

// Message is one dead letter.
type Message struct {
	ID         string
	Deliveries int64
	Reason     string
	BodyBytes  int
}

func (a *Admin) channel() (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.Dial(a.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to RabbitMQ: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, ch, nil
}

// List returns the depth of every dead-letter queue and, when queue is set, up to limit of its messages (peeked: they stay).
func (a *Admin) List(_ context.Context, queue string, limit int) ([]Depth, []Message, error) {
	if queue != "" && !known(queue) {
		return nil, nil, fmt.Errorf("%w %q", ErrUnknownQueue, queue)
	}
	var depths []Depth
	for _, q := range Queues() {
		// A failed passive declare closes the channel, so each queue gets its own.
		conn, ch, err := a.channel()
		if err != nil {
			return nil, nil, err
		}
		st, err := ch.QueueDeclarePassive(q+".dlq", true, false, false, false, amqp.Table{"x-queue-type": "quorum"})
		_ = conn.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("inspect %s.dlq: %w (is the topology declared?)", q, err)
		}
		depths = append(depths, Depth{Queue: q, Messages: int64(st.Messages)})
	}
	if queue == "" {
		return depths, nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	conn, ch, err := a.channel()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = conn.Close() }()
	var held []amqp.Delivery
	var msgs []Message
	for len(held) < limit {
		d, ok, err := ch.Get(queue+".dlq", false)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			break
		}
		held = append(held, d)
		m := Message{ID: d.MessageId, BodyBytes: len(d.Body)}
		switch n := d.Headers["x-delivery-count"].(type) {
		case int64:
			m.Deliveries = n
		case int32:
			m.Deliveries = int64(n)
		}
		if deaths, ok := d.Headers["x-death"].([]any); ok && len(deaths) > 0 {
			if t, ok := deaths[0].(amqp.Table); ok {
				m.Reason = fmt.Sprint(t["reason"])
			}
		}
		msgs = append(msgs, m)
	}
	for _, d := range held {
		_ = d.Nack(false, true) // peek only: put it back
	}
	return depths, msgs, nil
}

// Redrive moves up to limit dead letters of queue back to the exchange they came from. A message is acknowledged only after the
// broker confirmed its re-publication, so nothing is lost if this fails half way.
func (a *Admin) Redrive(_ context.Context, queue string, limit int) (int, error) {
	if !known(queue) {
		return 0, fmt.Errorf("%w %q", ErrUnknownQueue, queue)
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	conn, ch, err := a.channel()
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	if err := ch.Confirm(false); err != nil {
		return 0, err
	}
	exchange, key := rabbitx.ExchangeCommands, queue
	if queue == rabbitx.ReplyQueue {
		exchange, key = rabbitx.ExchangeReplies, rabbitx.ReplyRoutingKey
	}
	moved := 0
	for moved < limit {
		d, ok, err := ch.Get(queue+".dlq", false)
		if err != nil {
			return moved, err
		}
		if !ok {
			break
		}
		hdr := amqp.Table{"x-redriven-at": time.Now().UTC().Format(time.RFC3339)}
		for k, v := range d.Headers { // keep the trace context and the ids, drop the death records
			if k == "traceparent" || k == "tracestate" || strings.HasPrefix(k, "x-quotify") {
				hdr[k] = v
			}
		}
		dc, err := ch.PublishWithDeferredConfirm(exchange, key, true, false, amqp.Publishing{
			ContentType: d.ContentType, Body: d.Body, MessageId: d.MessageId, Headers: hdr, DeliveryMode: amqp.Persistent})
		if err != nil {
			_ = d.Nack(false, true)
			return moved, err
		}
		if !dc.Wait() {
			_ = d.Nack(false, true)
			return moved, errors.New("the broker did not confirm the re-publication")
		}
		if err := d.Ack(false); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}
