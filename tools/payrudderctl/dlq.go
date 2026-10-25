package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/0furkancolak/payrudder/pkg/rabbitx"
)

// dlq inspects and repairs the RabbitMQ dead-letter queues ({queue}.dlq, docs/04-messaging.md). Unlike the other
// commands it talks to the broker directly: dead letters are not part of the public API.
//
//	payrudderctl dlq ls
//	payrudderctl dlq show --queue payment.charge [--limit 5]
//	payrudderctl dlq redrive --queue payment.charge [--limit 100]
//	payrudderctl dlq purge --queue payment.charge --yes
//
// With --broker kafka the same ls|show|redrive work on the Kafka dead-letter topics (dlq_kafka.go).
func dlq(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: payrudderctl dlq ls|show|redrive|purge --broker rabbitmq [flags]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("dlq "+sub, flag.ContinueOnError)
	broker := fs.String("broker", "rabbitmq", "rabbitmq or kafka")
	topic := fs.String("topic", "", "Kafka dead-letter topic, e.g. usage.events.dlq.v1 (--broker kafka)")
	kafkaBrokers := fs.String("kafka-brokers", envOr("PAYRUDDER_KAFKA_BROKERS", "localhost:42092"), "Kafka bootstrap servers (--broker kafka)")
	url := fs.String("url", envOr("PAYRUDDER_RABBITMQ_URL", "amqp://payrudder:payrudder_local@localhost:42672/"), "AMQP URL")
	queue := fs.String("queue", "", "command queue, e.g. payment.charge, or billing-svc.replies")
	limit := fs.Int("limit", 10, "max messages")
	yes := fs.Bool("yes", false, "confirm purge")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	switch *broker {
	case "kafka":
		return dlqKafka(sub, strings.Split(*kafkaBrokers, ","), *topic, *limit)
	case "rabbitmq":
	default:
		return fmt.Errorf("--broker %s is not supported: rabbitmq or kafka", *broker)
	}
	conn, err := amqp.Dial(*url)
	if err != nil {
		return fmt.Errorf("connect to RabbitMQ: %w", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	switch sub {
	case "ls":
		return dlqList(ch)
	case "show", "redrive", "purge":
		if *queue == "" {
			return fmt.Errorf("--queue is required")
		}
		if !known(*queue) {
			return fmt.Errorf("unknown queue %q (want one of %s, %s)", *queue, strings.Join(rabbitx.CommandQueues, ", "), rabbitx.ReplyQueue)
		}
		switch sub {
		case "show":
			return dlqShow(ch, *queue, *limit)
		case "redrive":
			return dlqRedrive(ch, *queue, *limit)
		default:
			if !*yes {
				return fmt.Errorf("purge deletes every dead letter of %s; pass --yes", *queue)
			}
			n, err := ch.QueuePurge(*queue+".dlq", false)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "purged %d messages from %s.dlq\n", n, *queue)
			return nil
		}
	}
	return fmt.Errorf("unknown dlq command %q", sub)
}

func known(q string) bool {
	if q == rabbitx.ReplyQueue {
		return true
	}
	for _, c := range rabbitx.CommandQueues {
		if c == q {
			return true
		}
	}
	return false
}

func dlqList(ch *amqp.Channel) error {
	fmt.Fprintf(os.Stdout, "%-28s %s\n", "DEAD-LETTER QUEUE", "MESSAGES")
	for _, q := range append(append([]string(nil), rabbitx.CommandQueues...), rabbitx.ReplyQueue) {
		st, err := ch.QueueDeclarePassive(q+".dlq", true, false, false, false, amqp.Table{"x-queue-type": "quorum"})
		if err != nil {
			return fmt.Errorf("inspect %s.dlq: %w (is the topology declared?)", q, err)
		}
		fmt.Fprintf(os.Stdout, "%-28s %d\n", q+".dlq", st.Messages)
	}
	return nil
}

// dlqShow prints up to limit dead letters without removing them.
func dlqShow(ch *amqp.Channel, queue string, limit int) error {
	var held []amqp.Delivery
	for len(held) < limit {
		d, ok, err := ch.Get(queue+".dlq", false)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		held = append(held, d)
		fmt.Fprintf(os.Stdout, "--- message %d (id %q, %d bytes)\n", len(held), d.MessageId, len(d.Body))
		fmt.Fprintf(os.Stdout, "    original queue: %s, redeliveries: %v\n", queue, d.Headers["x-delivery-count"])
		if deaths, ok := d.Headers["x-death"].([]any); ok {
			for _, x := range deaths {
				if t, ok := x.(amqp.Table); ok {
					fmt.Fprintf(os.Stdout, "    died in %v, reason %v, %v times\n", t["queue"], t["reason"], t["count"])
				}
			}
		}
		fmt.Fprintf(os.Stdout, "    body (hex, first 64): %x\n", head(d.Body, 64))
	}
	for _, d := range held {
		_ = d.Nack(false, true) // peek only: put it back
	}
	fmt.Fprintf(os.Stdout, "%d message(s) shown\n", len(held))
	return nil
}

func head(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// dlqRedrive moves up to limit dead letters back to the original queue (or exchange, for replies) with the retry
// counter reset. A message is acked only after the broker confirmed its re-publication.
func dlqRedrive(ch *amqp.Channel, queue string, limit int) error {
	if err := ch.Confirm(false); err != nil {
		return err
	}
	exchange, key := rabbitx.ExchangeCommands, queue
	if queue == rabbitx.ReplyQueue {
		exchange, key = rabbitx.ExchangeReplies, rabbitx.ReplyRoutingKey
	}
	moved := 0
	for moved < limit {
		d, ok, err := ch.Get(queue+".dlq", false)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		hdr := amqp.Table{"x-redriven-at": time.Now().UTC().Format(time.RFC3339)}
		dc, err := ch.PublishWithDeferredConfirm(exchange, key, true, false, amqp.Publishing{
			ContentType: d.ContentType, Body: d.Body, MessageId: d.MessageId, Headers: hdr, DeliveryMode: amqp.Persistent})
		if err != nil {
			_ = d.Nack(false, true)
			return err
		}
		if acked := dc.Wait(); !acked {
			_ = d.Nack(false, true)
			return fmt.Errorf("the broker did not confirm the re-publication")
		}
		if err := d.Ack(false); err != nil {
			return err
		}
		moved++
	}
	fmt.Fprintf(os.Stdout, "redriven %d message(s) from %s.dlq to %s\n", moved, queue, exchange)
	if moved == limit {
		fmt.Fprintln(os.Stderr, "(limit reached; run again for more)")
	}
	return nil
}
