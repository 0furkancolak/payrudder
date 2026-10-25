//go:build integration

package itest

import (
	"context"
	"errors"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dlq"
)

// The operator sees the dead letters without taking them, and redriving moves them back to the queue they came from.
func testDeadLettersArePeekedAndRedriven(t *testing.T) {
	infra(t)
	if err := rabbitx.DeclareTopology(shared.amqp); err != nil {
		t.Fatal(err)
	}
	conn, err := amqp.Dial(shared.amqp)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	queue := rabbitx.QueueNotificationSend
	if _, err := ch.QueuePurge(queue+".dlq", false); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.QueuePurge(queue, false); err != nil {
		t.Fatal(err)
	}
	// A message straight into the dead-letter queue, as the broker would leave it after the retries ran out.
	if err := ch.PublishWithContext(context.Background(), "", queue+".dlq", false, false,
		amqp.Publishing{MessageId: "dead-1", Body: []byte("body"), DeliveryMode: amqp.Persistent}); err != nil {
		t.Fatal(err)
	}

	admin := &dlq.Admin{URL: shared.amqp}
	ctx := context.Background()
	var depths []dlq.Depth
	var msgs []dlq.Message
	testkit.Eventually(t, 15*time.Second, "the dead letter to be visible", func() bool {
		depths, msgs, err = admin.List(ctx, queue, 10)
		return err == nil && len(msgs) == 1
	})
	if msgs[0].ID != "dead-1" || msgs[0].BodyBytes != 4 {
		t.Errorf("message = %+v", msgs[0])
	}
	found := false
	for _, d := range depths {
		if d.Queue == queue {
			found = d.Messages == 1
		}
	}
	if !found || len(depths) != len(dlq.Queues()) {
		t.Errorf("depths = %+v, want one per queue and one message in %s", depths, queue)
	}
	// Peeking took nothing.
	if _, msgs, err = admin.List(ctx, queue, 10); err != nil || len(msgs) != 1 {
		t.Fatalf("second peek: %v, %v", msgs, err)
	}
	if _, _, err := admin.List(ctx, "nope", 10); !errors.Is(err, dlq.ErrUnknownQueue) {
		t.Errorf("an unknown queue: %v", err)
	}

	moved, err := admin.Redrive(ctx, queue, 10)
	if err != nil || moved != 1 {
		t.Fatalf("Redrive = %d, %v; want 1", moved, err)
	}
	if _, msgs, err = admin.List(ctx, queue, 10); err != nil || len(msgs) != 0 {
		t.Errorf("after the redrive: %v, %v; want the dead-letter queue empty", msgs, err)
	}
	st, err := ch.QueueInspect(queue)
	if err != nil || st.Messages != 1 {
		t.Errorf("queue after the redrive: %+v, %v; want the message back", st, err)
	}
}
