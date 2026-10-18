//go:build integration

package rabbitx_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/testkit"
)

func TestRabbitX(t *testing.T) {
	url := testkit.StartRabbitMQ(t)
	if err := rabbitx.DeclareTopology(url); err != nil {
		t.Fatal(err)
	}
	if err := rabbitx.DeclareTopology(url); err != nil { // idempotent
		t.Fatalf("second declare: %v", err)
	}
	pub := rabbitx.NewPublisher(url)
	defer pub.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	publish := func(queue, id string) {
		t.Helper()
		if err := pub.Publish(context.Background(), rabbitx.Message{Exchange: rabbitx.ExchangeCommands, RoutingKey: queue,
			Publishing: amqp.Publishing{MessageId: id, Body: []byte(id)}}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("UnroutableIsAnError", func(t *testing.T) {
		err := pub.Publish(context.Background(), rabbitx.Message{Exchange: rabbitx.ExchangeCommands, RoutingKey: "no.such.queue", Publishing: amqp.Publishing{Body: []byte("x")}})
		if !errors.Is(err, rabbitx.ErrUnroutable) {
			t.Fatalf("err = %v, want ErrUnroutable", err)
		}
	})

	t.Run("TransientErrorRetriesThroughTheDelayQueue", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		attempts := make(chan int, 4)
		go func() {
			_ = rabbitx.Consume(ctx, url, rabbitx.QueuePaymentStatus, 4, func(_ context.Context, d amqp.Delivery) rabbitx.Outcome {
				calls.Add(1)
				attempts <- rabbitx.Attempt(d)
				if rabbitx.Attempt(d) == 0 {
					return rabbitx.Retry
				}
				return rabbitx.Ack
			}, log)
		}()
		publish(rabbitx.QueuePaymentStatus, "retry-me")
		first := <-attempts
		start := time.Now()
		select {
		case second := <-attempts:
			if first != 0 || second != 1 {
				t.Fatalf("attempts = %d then %d, want 0 then 1", first, second)
			}
			if d := time.Since(start); d < 4*time.Second {
				t.Fatalf("retried after %s, want the 5 s tier", d)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the message was never redelivered")
		}
	})

	t.Run("RejectedMessageLandsInTheDLQ", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			_ = rabbitx.Consume(ctx, url, rabbitx.QueuePaymentRefund, 4, func(context.Context, amqp.Delivery) rabbitx.Outcome { return rabbitx.Reject }, log)
		}()
		publish(rabbitx.QueuePaymentRefund, "poison")
		conn, err := amqp.Dial(url)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ch, err := conn.Channel()
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			d, ok, err := ch.Get(rabbitx.QueuePaymentRefund+".dlq", true)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				if string(d.Body) != "poison" {
					t.Fatalf("dead letter body = %q", d.Body)
				}
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("the rejected message never reached payment.refund.dlq")
	})

	t.Run("ExhaustedRetriesDeadLetter", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			_ = rabbitx.Consume(ctx, url, rabbitx.QueueNotificationSend, 4, func(_ context.Context, d amqp.Delivery) rabbitx.Outcome {
				if rabbitx.Attempt(d) >= rabbitx.MaxAttempts {
					return rabbitx.Retry // the consumer refuses a 4th retry and dead-letters
				}
				return rabbitx.Retry
			}, log)
		}()
		// Start at the cap so the test does not wait for three delay tiers.
		if err := pub.Publish(context.Background(), rabbitx.Message{Exchange: rabbitx.ExchangeCommands, RoutingKey: rabbitx.QueueNotificationSend,
			Publishing: amqp.Publishing{Body: []byte("exhausted"), Headers: amqp.Table{rabbitx.AttemptHeader: int32(rabbitx.MaxAttempts)}}}); err != nil {
			t.Fatal(err)
		}
		conn, err := amqp.Dial(url)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ch, _ := conn.Channel()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if d, ok, _ := ch.Get(rabbitx.QueueNotificationSend+".dlq", true); ok && string(d.Body) == "exhausted" {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("exhausted message never reached the dlq")
	})
}
