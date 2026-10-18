// Package rabbitx is Quotify's RabbitMQ toolkit: the single definition of the topology (docs/04-messaging.md), a
// publisher with confirms, and a consumer with manual acks and the retry/dead-letter policy.
package rabbitx

import (
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Exchanges.
const (
	ExchangeCommands   = "billing.commands"
	ExchangeRetry      = "billing.commands.retry"
	ExchangeDLX        = "billing.commands.dlx"
	ExchangeReplies    = "billing.replies"
	ExchangeRepliesDLX = "billing.replies.dlx"
)

// Command queues (also the routing keys on billing.commands).
const (
	QueueInvoiceRender    = "invoice.render"
	QueuePaymentCharge    = "payment.charge"
	QueuePaymentRefund    = "payment.refund"
	QueuePaymentStatus    = "payment.status"
	QueueNotificationSend = "notification.send"
)

// ReplyQueue is billing-svc's reply queue; ReplyRoutingKey binds it to billing.replies.
const (
	ReplyQueue      = "billing-svc.replies"
	ReplyRoutingKey = "reply"
)

// DeliveryLimit is the quorum queues' x-delivery-limit: a message redelivered this often is dead-lettered, which is what
// ends a worker crash loop.
const DeliveryLimit = 5

// CommandQueues lists the five command queues.
var CommandQueues = []string{QueueInvoiceRender, QueuePaymentCharge, QueuePaymentRefund, QueuePaymentStatus, QueueNotificationSend}

// Tier is one delay queue of the retry topology.
type Tier struct {
	Name  string
	Delay time.Duration
}

// Tiers are the retry delays, by attempt (the first retry waits 5 s).
var Tiers = []Tier{{"5s", 5 * time.Second}, {"30s", 30 * time.Second}, {"2m", 2 * time.Minute}}

// MaxAttempts is how often a worker retries a transient error before dead-lettering.
const MaxAttempts = 3

// RetryRoutingKey is the routing key on the retry exchange for the nth retry (1-based) of a queue.
func RetryRoutingKey(queue string, retry int) string {
	return fmt.Sprintf("%s.retry.%s", queue, Tiers[min(max(retry, 1), len(Tiers))-1].Name)
}

type exchange struct{ Name, Kind string }

type queue struct {
	Name      string
	Arguments amqp.Table
	Bindings  []binding
}

type binding struct{ Exchange, Key string }

func quorum(dlx, dlrk string) amqp.Table {
	return amqp.Table{"x-queue-type": "quorum", "x-dead-letter-exchange": dlx, "x-dead-letter-routing-key": dlrk, "x-delivery-limit": int64(DeliveryLimit)}
}

func exchanges() []exchange {
	return []exchange{
		{ExchangeCommands, "direct"}, {ExchangeRetry, "direct"}, {ExchangeDLX, "direct"},
		{ExchangeReplies, "direct"}, {ExchangeRepliesDLX, "direct"},
	}
}

func queues() []queue {
	var out []queue
	for _, q := range CommandQueues {
		out = append(out, queue{Name: q, Arguments: quorum(ExchangeDLX, q), Bindings: []binding{{ExchangeCommands, q}}})
		out = append(out, queue{Name: q + ".dlq", Arguments: amqp.Table{"x-queue-type": "quorum"}, Bindings: []binding{{ExchangeDLX, q}}})
		for _, t := range Tiers {
			out = append(out, queue{
				Name:      fmt.Sprintf("%s.retry.%s", q, t.Name),
				Arguments: amqp.Table{"x-message-ttl": t.Delay.Milliseconds(), "x-dead-letter-exchange": ExchangeCommands, "x-dead-letter-routing-key": q},
				Bindings:  []binding{{ExchangeRetry, fmt.Sprintf("%s.retry.%s", q, t.Name)}},
			})
		}
	}
	out = append(out,
		queue{Name: ReplyQueue, Arguments: quorum(ExchangeRepliesDLX, ReplyRoutingKey), Bindings: []binding{{ExchangeReplies, ReplyRoutingKey}}},
		queue{Name: ReplyQueue + ".dlq", Arguments: amqp.Table{"x-queue-type": "quorum"}, Bindings: []binding{{ExchangeRepliesDLX, ReplyRoutingKey}}})
	return out
}

// Declare creates the whole topology. It is idempotent and safe to call from every service at start-up.
func Declare(ch *amqp.Channel) error {
	for _, e := range exchanges() {
		if err := ch.ExchangeDeclare(e.Name, e.Kind, true, false, false, false, nil); err != nil {
			return fmt.Errorf("rabbitx: declare exchange %s: %w", e.Name, err)
		}
	}
	for _, q := range queues() {
		if _, err := ch.QueueDeclare(q.Name, true, false, false, false, q.Arguments); err != nil {
			return fmt.Errorf("rabbitx: declare queue %s: %w", q.Name, err)
		}
		for _, b := range q.Bindings {
			if err := ch.QueueBind(q.Name, b.Key, b.Exchange, false, nil); err != nil {
				return fmt.Errorf("rabbitx: bind %s to %s: %w", q.Name, b.Exchange, err)
			}
		}
	}
	return nil
}

// Definitions renders the topology as a RabbitMQ definitions file (deploy/rabbitmq/definitions.json), for inspection
// and for importing into a broker. A test keeps the committed file in sync with Declare.
func Definitions() ([]byte, error) {
	type jq struct {
		Name       string         `json:"name"`
		Vhost      string         `json:"vhost"`
		Durable    bool           `json:"durable"`
		AutoDelete bool           `json:"auto_delete"`
		Arguments  map[string]any `json:"arguments"`
	}
	type je struct {
		Name       string         `json:"name"`
		Vhost      string         `json:"vhost"`
		Type       string         `json:"type"`
		Durable    bool           `json:"durable"`
		AutoDelete bool           `json:"auto_delete"`
		Internal   bool           `json:"internal"`
		Arguments  map[string]any `json:"arguments"`
	}
	type jb struct {
		Source          string `json:"source"`
		Vhost           string `json:"vhost"`
		Destination     string `json:"destination"`
		DestinationType string `json:"destination_type"`
		RoutingKey      string `json:"routing_key"`
	}
	// The local-development login lives next to the topology so that one file seeds a fresh broker.
	type juser struct {
		Name     string   `json:"name"`
		Password string   `json:"password"`
		Tags     []string `json:"tags"`
	}
	type jvhost struct {
		Name string `json:"name"`
	}
	type jperm struct {
		User      string `json:"user"`
		Vhost     string `json:"vhost"`
		Configure string `json:"configure"`
		Write     string `json:"write"`
		Read      string `json:"read"`
	}
	doc := struct {
		Users       []juser  `json:"users"`
		Vhosts      []jvhost `json:"vhosts"`
		Permissions []jperm  `json:"permissions"`
		Queues      []jq     `json:"queues"`
		Exchanges   []je     `json:"exchanges"`
		Bindings    []jb     `json:"bindings"`
	}{
		Users:       []juser{{Name: "quotify", Password: "quotify_local", Tags: []string{"administrator"}}}, //nolint:gosec // local-development login only
		Vhosts:      []jvhost{{Name: "/"}},
		Permissions: []jperm{{User: "quotify", Vhost: "/", Configure: ".*", Write: ".*", Read: ".*"}},
		Queues:      []jq{}, Exchanges: []je{}, Bindings: []jb{},
	}
	for _, e := range exchanges() {
		doc.Exchanges = append(doc.Exchanges, je{Name: e.Name, Vhost: "/", Type: e.Kind, Durable: true, Arguments: map[string]any{}})
	}
	for _, q := range queues() {
		doc.Queues = append(doc.Queues, jq{Name: q.Name, Vhost: "/", Durable: true, Arguments: q.Arguments})
		for _, b := range q.Bindings {
			doc.Bindings = append(doc.Bindings, jb{Source: b.Exchange, Vhost: "/", Destination: q.Name, DestinationType: "queue", RoutingKey: b.Key})
		}
	}
	return json.MarshalIndent(doc, "", "  ")
}
