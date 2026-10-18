//go:build integration

package workerx_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/testkit"
	"github.com/0furkancolak/quotify/pkg/workerx"
)

type memInbox struct {
	mu sync.Mutex
	m  map[uuid.UUID]*commandsv1.ReplyEnvelope
}

func (i *memInbox) Lookup(_ context.Context, id uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	r, ok := i.m[id]
	return r, ok, nil
}

func (i *memInbox) Store(_ context.Context, id uuid.UUID, r *commandsv1.ReplyEnvelope) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.m[id] = r
	return nil
}

func notifyCommand(id uuid.UUID, to string) []byte {
	b, _ := proto.Marshal(&commandsv1.CommandEnvelope{CommandId: id.String(), SagaId: uuid.NewString(), TenantId: uuid.NewString(),
		Command: &commandsv1.CommandEnvelope_SendNotification{SendNotification: &commandsv1.SendNotification{Template: "t", To: to}}})
	return b
}

// drain returns the replies on billing-svc.replies, polling for up to wait.
func drain(t *testing.T, ch *amqp.Channel, want int, wait time.Duration) []*commandsv1.ReplyEnvelope {
	t.Helper()
	var out []*commandsv1.ReplyEnvelope
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) && len(out) < want {
		d, ok, err := ch.Get(rabbitx.ReplyQueue, true)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		var r commandsv1.ReplyEnvelope
		if err := proto.Unmarshal(d.Body, &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, &r)
	}
	return out
}

func TestWorker(t *testing.T) {
	url := testkit.StartRabbitMQ(t)
	if err := rabbitx.DeclareTopology(url); err != nil {
		t.Fatal(err)
	}
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	pub := rabbitx.NewPublisher(url)
	defer pub.Close()
	send := func(queue string, body []byte) {
		t.Helper()
		if err := pub.Publish(context.Background(), rabbitx.Message{Exchange: rabbitx.ExchangeCommands, RoutingKey: queue, Publishing: amqp.Publishing{Body: body}}); err != nil {
			t.Fatal(err)
		}
	}
	dlq := func(queue string, wait time.Duration) bool {
		deadline := time.Now().Add(wait)
		for time.Now().Before(deadline) {
			if _, ok, _ := ch.Get(queue+".dlq", true); ok {
				return true
			}
			time.Sleep(200 * time.Millisecond)
		}
		return false
	}
	run := func(queue string, h workerx.Handler, inbox workerx.Inbox) context.CancelFunc {
		ctx, cancel := context.WithCancel(context.Background())
		w := &workerx.Worker{URL: url, Queues: []string{queue}, Handler: h, Inbox: inbox, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		go func() { _ = w.Run(ctx) }()
		t.Cleanup(cancel)
		return cancel
	}
	ok := func(*commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_NotificationSent{NotificationSent: &commandsv1.NotificationSent{MessageId: "m"}}}, nil
	}

	t.Run("RedeliveryIsAnsweredFromTheInboxWithoutRunningTheHandlerAgain", func(t *testing.T) {
		var calls atomic.Int32
		run(rabbitx.QueueNotificationSend, workerx.HandlerFunc(func(_ context.Context, c *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
			calls.Add(1)
			return ok(c)
		}), &memInbox{m: map[uuid.UUID]*commandsv1.ReplyEnvelope{}})
		id := uuid.New()
		send(rabbitx.QueueNotificationSend, notifyCommand(id, "a@x.io"))
		send(rabbitx.QueueNotificationSend, notifyCommand(id, "a@x.io")) // redelivery of the same command
		replies := drain(t, ch, 2, 20*time.Second)
		if len(replies) != 2 || replies[0].GetReplyId() != replies[1].GetReplyId() || replies[0].GetCommandId() != id.String() {
			t.Fatalf("replies = %v, want the same reply twice", replies)
		}
		if calls.Load() != 1 {
			t.Fatalf("the handler ran %d times, want 1", calls.Load())
		}
		if replies[0].GetReplyId() != workerx.ReplyID(id.String()) {
			t.Fatal("the reply id is not the deterministic one")
		}
	})

	t.Run("UndecodableCommandGoesStraightToTheDLQWithoutAReply", func(t *testing.T) {
		run(rabbitx.QueuePaymentStatus, workerx.HandlerFunc(func(context.Context, *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
			t.Error("handler called for garbage")
			return nil, nil
		}), nil)
		send(rabbitx.QueuePaymentStatus, []byte{0xff, 0xfe, 0x01})
		if !dlq(rabbitx.QueuePaymentStatus, 20*time.Second) {
			t.Fatal("garbage never reached payment.status.dlq")
		}
		if r := drain(t, ch, 1, time.Second); len(r) != 0 {
			t.Fatalf("a reply was sent for garbage: %v", r)
		}
	})

	t.Run("PoisonErrorIsDeadLetteredAtOnce", func(t *testing.T) {
		run(rabbitx.QueuePaymentRefund, workerx.HandlerFunc(func(context.Context, *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
			return nil, workerx.ErrPoison
		}), nil)
		send(rabbitx.QueuePaymentRefund, notifyCommand(uuid.New(), "x"))
		if !dlq(rabbitx.QueuePaymentRefund, 20*time.Second) {
			t.Fatal("poison command never reached the dlq")
		}
	})

	t.Run("AHandlerThatPanicsIsDeadLetteredAfterFiveDeliveries", func(t *testing.T) {
		var deliveries atomic.Int32
		run(rabbitx.QueuePaymentCharge, workerx.HandlerFunc(func(context.Context, *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
			deliveries.Add(1)
			panic("boom")
		}), nil)
		send(rabbitx.QueuePaymentCharge, notifyCommand(uuid.New(), "x"))
		if !dlq(rabbitx.QueuePaymentCharge, 60*time.Second) {
			t.Fatalf("the crashing command never reached the dlq (%d deliveries)", deliveries.Load())
		}
		// x-delivery-limit counts returns: the first delivery plus DeliveryLimit redeliveries.
		if n := deliveries.Load(); n != rabbitx.DeliveryLimit+1 {
			t.Fatalf("delivered %d times before dead-lettering, want %d (1 + the delivery limit)", n, rabbitx.DeliveryLimit+1)
		}
		if r := drain(t, ch, 1, time.Second); len(r) != 0 {
			t.Fatalf("a reply was sent for a crashing command: %v", r)
		}
	})

	t.Run("TransientErrorRetriesAndThenReplies", func(t *testing.T) {
		var calls atomic.Int32
		cancel := run(rabbitx.QueueInvoiceRender, workerx.HandlerFunc(func(_ context.Context, c *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
			if calls.Add(1) == 1 {
				return nil, errors.New("storage unavailable")
			}
			return ok(c)
		}), nil)
		defer cancel()
		send(rabbitx.QueueInvoiceRender, notifyCommand(uuid.New(), "x"))
		if r := drain(t, ch, 1, 30*time.Second); len(r) != 1 || calls.Load() != 2 {
			t.Fatalf("replies = %v after %d calls, want one reply after the 5 s retry", r, calls.Load())
		}
	})
}
