// Package workerx is the skeleton of Payrudder's RabbitMQ workers (docs/03-services/workers.md): decode the command
// envelope, answer redeliveries from an inbox, run the handler, classify its error, publish the reply with confirms and
// only then ack the command.
package workerx

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
)

// Handler executes one command and returns the reply to publish.
//
// A business failure (a declined card, a template error) is a reply, not an error. An error means the command could not
// be executed now: it is retried through the delay queues (5 s, 30 s, 2 min) and dead-lettered when they are used up.
type Handler interface {
	Handle(ctx context.Context, cmd *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error)
}

// HandlerFunc adapts a function.
type HandlerFunc func(ctx context.Context, cmd *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error)

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, cmd *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
	return f(ctx, cmd)
}

// ErrPoison marks a command that can never be executed (unknown type, missing fields). It is dead-lettered at once and
// no reply is sent: billing-svc's step deadline notices.
var ErrPoison = errors.New("workerx: poison command")

// Inbox remembers the reply to each executed command, so a redelivered command gets the same reply without running
// its side effect again. The handler is free to store the reply in its own transaction instead and implement Lookup on it.
type Inbox interface {
	Lookup(ctx context.Context, commandID uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error)
	Store(ctx context.Context, commandID uuid.UUID, reply *commandsv1.ReplyEnvelope) error
}

// TenantInbox is an Inbox that keeps its replies where the tenant's data is (cells, ADR 0018) and so has to know whose command it is. A Worker whose Inbox is one
// uses LookupFor and StoreFor, with the tenant of the command envelope (the nil UUID when it carries none), instead of Lookup and Store.
type TenantInbox interface {
	Inbox
	LookupFor(ctx context.Context, tenant, commandID uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error)
	StoreFor(ctx context.Context, tenant, commandID uuid.UUID, reply *commandsv1.ReplyEnvelope) error
}

// Worker consumes command queues.
type Worker struct {
	URL      string
	Queues   []string
	Prefetch int // default 16
	Handler  Handler
	Inbox    Inbox // optional
	Log      *slog.Logger

	pubOnce sync.Once
	pub     *rabbitx.Publisher

	locks KeyedMutex // by command id: duplicates of one command in this process run one at a time

	handled  metric.Int64Counter
	replayed metric.Int64Counter
}

var replyNS = uuid.MustParse("1f4d7a52-9c3b-5e08-b6a1-2d8e0f5c7a93")

// ReplyID is the deterministic id of the reply to a command.
func ReplyID(commandID string) string { return uuid.NewSHA1(replyNS, []byte(commandID)).String() }

func (w *Worker) init() error {
	var err error
	w.pubOnce.Do(func() {
		if w.Log == nil {
			w.Log = slog.Default()
		}
		if w.Prefetch <= 0 {
			w.Prefetch = 16
		}
		w.pub = rabbitx.NewPublisher(w.URL)
		m := otel.Meter("github.com/0furkancolak/payrudder/pkg/workerx")
		if w.handled, err = m.Int64Counter("worker_commands_handled", metric.WithDescription("Commands handled, by queue and result")); err != nil {
			return
		}
		w.replayed, err = m.Int64Counter("worker_commands_replayed", metric.WithDescription("Redelivered commands answered from the inbox"))
	})
	return err
}

// Run declares the topology and consumes the queues until ctx is done.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.init(); err != nil {
		return err
	}
	if err := rabbitx.DeclareTopology(w.URL); err != nil {
		return err
	}
	defer w.pub.Close()
	var wg sync.WaitGroup
	for _, q := range w.Queues {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rabbitx.Consume(ctx, w.URL, q, w.Prefetch, func(ctx context.Context, d amqp.Delivery) rabbitx.Outcome {
				return w.handle(ctx, q, d)
			}, w.Log)
		}()
	}
	wg.Wait()
	return nil
}

// Ping checks the broker connection (readiness).
func (w *Worker) Ping() error {
	if err := w.init(); err != nil {
		return err
	}
	return w.pub.Ping()
}

func (w *Worker) result(ctx context.Context, queue, result string) {
	w.handled.Add(ctx, 1, metric.WithAttributes(attribute.String("queue", queue), attribute.String("result", result)))
}

func (w *Worker) handle(ctx context.Context, queue string, d amqp.Delivery) rabbitx.Outcome {
	var cmd commandsv1.CommandEnvelope
	if err := proto.Unmarshal(d.Body, &cmd); err != nil || cmd.GetCommand() == nil {
		w.Log.WarnContext(ctx, "undecodable command, dead-lettering", "queue", queue, "error", err)
		w.result(ctx, queue, "poison")
		return rabbitx.Reject
	}
	commandID, err := uuid.Parse(cmd.GetCommandId())
	if err != nil {
		w.result(ctx, queue, "poison")
		return rabbitx.Reject
	}

	w.Log.DebugContext(ctx, "command received", "queue", queue, "command_id", cmd.GetCommandId(), "attempt", rabbitx.Attempt(d))

	// Two deliveries of the same command can be in flight in this process (prefetch > 1): run them one after the other,
	// so the second finds the first one's reply in the inbox. Across processes the handlers' own idempotency applies
	// (deterministic object keys, PSP idempotency keys, the notification claim).
	defer w.locks.Lock(commandID.String())()

	tenantID, _ := uuid.Parse(cmd.GetTenantId())
	if w.Inbox != nil {
		reply, found, err := w.lookup(ctx, tenantID, commandID)
		if err != nil {
			w.result(ctx, queue, "retry")
			return rabbitx.Retry
		}
		if found {
			w.replayed.Add(ctx, 1, metric.WithAttributes(attribute.String("queue", queue)))
			return w.publish(ctx, queue, &cmd, reply)
		}
	}

	reply, err := w.Handler.Handle(ctx, &cmd)
	switch {
	case errors.Is(err, ErrPoison):
		w.Log.WarnContext(ctx, "poison command, dead-lettering", "queue", queue, "command_id", cmd.GetCommandId(), "error", err)
		w.result(ctx, queue, "poison")
		return rabbitx.Reject
	case err != nil:
		w.Log.WarnContext(ctx, "command failed, retrying", "queue", queue, "command_id", cmd.GetCommandId(), "attempt", rabbitx.Attempt(d), "error", err)
		w.result(ctx, queue, "retry")
		return rabbitx.Retry
	case reply == nil:
		w.result(ctx, queue, "no_reply")
		return rabbitx.Ack
	}
	reply.CommandId, reply.SagaId, reply.TenantId = cmd.GetCommandId(), cmd.GetSagaId(), cmd.GetTenantId()
	reply.ReplyId = ReplyID(cmd.GetCommandId())
	if w.Inbox != nil {
		if err := w.store(ctx, tenantID, commandID, reply); err != nil {
			w.Log.WarnContext(ctx, "could not store the reply, retrying", "error", err)
			w.result(ctx, queue, "retry")
			return rabbitx.Retry
		}
	}
	return w.publish(ctx, queue, &cmd, reply)
}

func (w *Worker) lookup(ctx context.Context, tenant, commandID uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error) {
	if ti, ok := w.Inbox.(TenantInbox); ok {
		return ti.LookupFor(ctx, tenant, commandID)
	}
	return w.Inbox.Lookup(ctx, commandID)
}

func (w *Worker) store(ctx context.Context, tenant, commandID uuid.UUID, reply *commandsv1.ReplyEnvelope) error {
	if ti, ok := w.Inbox.(TenantInbox); ok {
		return ti.StoreFor(ctx, tenant, commandID, reply)
	}
	return w.Inbox.Store(ctx, commandID, reply)
}

func (w *Worker) publish(ctx context.Context, queue string, cmd *commandsv1.CommandEnvelope, reply *commandsv1.ReplyEnvelope) rabbitx.Outcome {
	body, err := proto.Marshal(reply)
	if err != nil {
		return rabbitx.Reject
	}
	if err := w.pub.Publish(ctx, rabbitx.Message{
		Exchange: rabbitx.ExchangeReplies, RoutingKey: rabbitx.ReplyRoutingKey,
		Publishing: amqp.Publishing{ContentType: "application/x-protobuf", Body: body, MessageId: reply.GetReplyId()},
	}); err != nil {
		w.Log.WarnContext(ctx, "reply publish failed, requeueing the command", "queue", queue, "command_id", cmd.GetCommandId(), "error", err)
		w.result(ctx, queue, "requeue")
		return rabbitx.Requeue
	}
	w.result(ctx, queue, "ok")
	return rabbitx.Ack
}
