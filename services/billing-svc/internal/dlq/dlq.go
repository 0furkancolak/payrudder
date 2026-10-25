// Package dlq inspects and redrives the RabbitMQ dead-letter queues ({queue}.dlq, docs/04-messaging.md) for the operator API.
// It opens a connection per call: this is an operator action, not a hot path.
package dlq

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/0furkancolak/payrudder/pkg/rabbitx"
)

// ErrUnknownQueue is returned for a queue that has no dead-letter queue.
var ErrUnknownQueue = errors.New("dlq: unknown queue")

// Limits of one call.
const (
	dialTimeout = 10 * time.Second
	// listDefault and listMax bound how many dead letters List shows; redriveDefault and redriveMax how many Redrive moves.
	listDefault    = 20
	listMax        = 100
	redriveDefault = 100
	redriveMax     = 1000
)

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

// dial connects to the broker; the connection attempt ends with ctx (or after dialTimeout).
func (a *Admin) dial(ctx context.Context) (*amqp.Connection, error) {
	conn, err := amqp.DialConfig(a.URL, amqp.Config{
		Heartbeat: 10 * time.Second,
		Locale:    "en_US",
		Dial: func(network, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: dialTimeout}
			return d.DialContext(ctx, network, addr)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("connect to RabbitMQ: %w", err)
	}
	return conn, nil
}

// depth is the number of messages in the dead-letter queue of q. A failed passive declare closes its channel, so each question gets its own channel.
func depth(conn *amqp.Connection, q string) (int64, error) {
	ch, err := conn.Channel()
	if err != nil {
		return 0, err
	}
	defer func() { _ = ch.Close() }()
	st, err := ch.QueueDeclarePassive(q+".dlq", true, false, false, false, amqp.Table{"x-queue-type": "quorum"})
	if err != nil {
		return 0, fmt.Errorf("inspect %s.dlq: %w (is the topology declared?)", q, err)
	}
	return int64(st.Messages), nil
}

// messageOf describes a dead letter from its headers: how often it was delivered and why the broker gave up on it.
func messageOf(d amqp.Delivery) Message {
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
	return m
}

// List returns the depth of every dead-letter queue and, when queue is set, up to limit of its messages (peeked: they stay).
func (a *Admin) List(ctx context.Context, queue string, limit int) ([]Depth, []Message, error) {
	if queue != "" && !known(queue) {
		return nil, nil, fmt.Errorf("%w %q", ErrUnknownQueue, queue)
	}
	conn, err := a.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = conn.Close() }()
	var depths []Depth
	for _, q := range Queues() {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		n, err := depth(conn, q)
		if err != nil {
			return nil, nil, err
		}
		depths = append(depths, Depth{Queue: q, Messages: n})
	}
	if queue == "" {
		return depths, nil, nil
	}
	if limit <= 0 || limit > listMax {
		limit = listDefault
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, nil, err
	}
	var held []amqp.Delivery
	var msgs []Message
	for len(held) < limit {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		d, ok, err := ch.Get(queue+".dlq", false)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			break
		}
		held = append(held, d)
		msgs = append(msgs, messageOf(d))
	}
	for _, d := range held {
		_ = d.Nack(false, true) // peek only: put it back
	}
	return depths, msgs, nil
}

// redriveHeaders are the headers a redriven message keeps: the trace context and the ids, not the death records.
func redriveHeaders(d amqp.Delivery) amqp.Table {
	hdr := amqp.Table{"x-redriven-at": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range d.Headers {
		if k == "traceparent" || k == "tracestate" || strings.HasPrefix(k, "x-payrudder") {
			hdr[k] = v
		}
	}
	return hdr
}

// Redrive moves up to limit dead letters of queue back to the exchange they came from. A message is acknowledged only after the
// broker confirmed its re-publication, so nothing is lost if this fails half way.
func (a *Admin) Redrive(ctx context.Context, queue string, limit int) (int, error) {
	if !known(queue) {
		return 0, fmt.Errorf("%w %q", ErrUnknownQueue, queue)
	}
	if limit <= 0 {
		limit = redriveDefault
	}
	limit = min(limit, redriveMax)
	conn, err := a.dial(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return 0, err
	}
	if err := ch.Confirm(false); err != nil {
		return 0, err
	}
	exchange, key := rabbitx.ExchangeCommands, queue
	if queue == rabbitx.ReplyQueue {
		exchange, key = rabbitx.ExchangeReplies, rabbitx.ReplyRoutingKey
	}
	moved := 0
	for moved < limit {
		if err := ctx.Err(); err != nil {
			return moved, err
		}
		d, ok, err := ch.Get(queue+".dlq", false)
		if err != nil {
			return moved, err
		}
		if !ok {
			break
		}
		dc, err := ch.PublishWithDeferredConfirm(exchange, key, true, false, amqp.Publishing{
			ContentType: d.ContentType, Body: d.Body, MessageId: d.MessageId, Headers: redriveHeaders(d), DeliveryMode: amqp.Persistent})
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
