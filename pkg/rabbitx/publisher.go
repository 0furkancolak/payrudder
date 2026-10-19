package rabbitx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"

	"github.com/0furkancolak/quotify/pkg/otelx"
)

// Message is one publication.
type Message struct {
	Exchange   string
	RoutingKey string
	Publishing amqp.Publishing
}

// ErrUnroutable means the broker returned a mandatory message: no queue is bound for its routing key.
var ErrUnroutable = errors.New("rabbitx: message was not routed to any queue")

// Publisher publishes with confirms. It owns one connection and channel and re-dials after a failure; callers retry.
type Publisher struct {
	url string

	mu      sync.Mutex
	conn    *amqp.Connection
	ch      *amqp.Channel
	returns chan amqp.Return
}

// NewPublisher returns a Publisher for the AMQP URL. It connects lazily.
func NewPublisher(url string) *Publisher { return &Publisher{url: url} }

func (p *Publisher) open() error {
	if p.ch != nil && !p.ch.IsClosed() {
		return nil
	}
	if p.conn == nil || p.conn.IsClosed() {
		c, err := amqp.DialConfig(p.url, amqp.Config{Heartbeat: 10 * time.Second, Locale: "en_US", Dial: amqp.DefaultDial(5 * time.Second)})
		if err != nil {
			return fmt.Errorf("rabbitx: dial: %w", err)
		}
		p.conn = c
	}
	ch, err := p.conn.Channel()
	if err != nil {
		_ = p.conn.Close()
		p.conn = nil
		return fmt.Errorf("rabbitx: channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return fmt.Errorf("rabbitx: confirm mode: %w", err)
	}
	p.returns = ch.NotifyReturn(make(chan amqp.Return, 64))
	p.ch = ch
	return nil
}

func (p *Publisher) reset() {
	if p.ch != nil {
		_ = p.ch.Close()
		p.ch = nil
	}
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

// Publish sends the messages and returns nil only when the broker has confirmed every one of them and none was
// returned as unroutable. Messages are published mandatory and persistent.
func (p *Publisher) Publish(ctx context.Context, msgs ...Message) error {
	if len(msgs) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.open(); err != nil {
		return err
	}
	confirms := make([]*amqp.DeferredConfirmation, len(msgs))
	for i, m := range msgs {
		pub := m.Publishing
		pub.DeliveryMode = amqp.Persistent
		// Carry the trace on: unless the message already has a context (the relay stores the originating one), use ctx's.
		if _, has := pub.Headers["traceparent"]; !has {
			hdr := amqp.Table{}
			for k, v := range pub.Headers {
				hdr[k] = v
			}
			otel.GetTextMapPropagator().Inject(ctx, otelx.AMQPTableCarrier(hdr))
			pub.Headers = hdr
		}
		dc, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, m.Exchange, m.RoutingKey, true, false, pub)
		if err != nil {
			p.reset()
			return fmt.Errorf("rabbitx: publish: %w", err)
		}
		confirms[i] = dc
	}
	for _, dc := range confirms {
		ok, err := dc.WaitContext(ctx)
		if err != nil {
			p.reset()
			return fmt.Errorf("rabbitx: wait for confirm: %w", err)
		}
		if !ok {
			return errors.New("rabbitx: broker nacked a publication")
		}
	}
	select {
	case r := <-p.returns:
		return fmt.Errorf("%w: exchange %q key %q (%s)", ErrUnroutable, r.Exchange, r.RoutingKey, r.ReplyText)
	default:
	}
	return nil
}

// Close closes the connection.
func (p *Publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reset()
}

// Ping checks that the publisher can reach the broker.
func (p *Publisher) Ping() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.open(); err != nil {
		p.reset()
		return err
	}
	return nil
}

// DeclareTopology dials the broker and declares the topology (idempotent).
func DeclareTopology(url string) error {
	conn, err := amqp.DialConfig(url, amqp.Config{Heartbeat: 10 * time.Second, Locale: "en_US", Dial: amqp.DefaultDial(5 * time.Second)})
	if err != nil {
		return fmt.Errorf("rabbitx: dial: %w", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	return Declare(ch)
}
