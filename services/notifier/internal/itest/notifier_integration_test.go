//go:build integration

// Package itest runs notifier against real Postgres, RabbitMQ, Redpanda and Mailpit.
package itest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
	"github.com/0furkancolak/payrudder/services/notifier/internal/handler"
	"github.com/0furkancolak/payrudder/services/notifier/internal/limits"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
	"github.com/0furkancolak/payrudder/services/notifier/migrations"
)

type message struct {
	Subject string
	To      []struct{ Address string }
}

// mailbox returns the messages Mailpit holds for a recipient.
func mailbox(t *testing.T, api, to string) []message {
	t.Helper()
	resp, err := http.Get(api + "/api/v1/search?query=" + url.QueryEscape("to:"+to))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Messages []message `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Messages
}

func waitMail(t *testing.T, api, to string, want int) []message {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if m := mailbox(t, api, to); len(m) >= want {
			return m
		}
		time.Sleep(200 * time.Millisecond)
	}
	return mailbox(t, api, to)
}

// offSwitch is a mail switch with the categories in it switched off.
type offSwitch map[string]bool

func (o offSwitch) Enabled(_ context.Context, _ uuid.UUID, category string) (bool, error) {
	return !o[category], nil
}

type fakePlans struct{ email string }

func (f fakePlans) GetCustomer(_ context.Context, in *plansv1.GetCustomerRequest, _ ...grpc.CallOption) (*plansv1.GetCustomerResponse, error) {
	return &plansv1.GetCustomerResponse{Customer: &plansv1.Customer{Id: in.GetId(), Name: "Ada", Email: f.email}}, nil
}

type fakeAdmin struct{ env plansv1.Environment }

func (f fakeAdmin) GetTenant(_ context.Context, in *plansv1.GetTenantRequest, _ ...grpc.CallOption) (*plansv1.GetTenantResponse, error) {
	return &plansv1.GetTenantResponse{Tenant: &plansv1.Tenant{Id: in.GetTenantId(), Name: "Acme", Environment: f.env}}, nil
}

func TestNotifier(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "notifier", migrations.FS)
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	mp := testkit.StartMailpit(t)
	amqpURL := testkit.StartRabbitMQ(t)
	brokers := testkit.StartRedpanda(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	notes := store.Notifications{DB: pool, Stale: 300 * time.Millisecond}
	mailer := &delivery.Mailer{Default: mailx.SMTP{Addr: mp.SMTPAddr}, From: "Payrudder <billing@payrudder.local>", Notes: notes}

	t.Run("RedeliveredNotificationSendsOneEmail", func(t *testing.T) {
		if err := rabbitx.DeclareTopology(amqpURL); err != nil {
			t.Fatal(err)
		}
		conn, _ := amqp.Dial(amqpURL)
		defer conn.Close()
		ch, _ := conn.Channel()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w := &workerx.Worker{URL: amqpURL, Queues: []string{rabbitx.QueueNotificationSend}, Handler: &handler.Handler{Mailer: mailer}, Inbox: notes, Log: log}
		go func() { _ = w.Run(ctx) }()
		pub := rabbitx.NewPublisher(amqpURL)
		defer pub.Close()

		to := "redelivery-" + uuid.NewString()[:8] + "@example.com"
		id := uuid.New()
		body, _ := proto.Marshal(&commandsv1.CommandEnvelope{CommandId: id.String(), SagaId: uuid.NewString(), TenantId: uuid.NewString(),
			Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_SendNotification{SendNotification: &commandsv1.SendNotification{Template: "invoice_paid.v1", To: to,
				Data: map[string]string{"customer_name": "Ada", "invoice_number": "INV-2026-000001", "amount": "10.00", "currency": "USD", "period": "2026-10", "tenant_name": "Acme"}}}})
		for range 3 { // the original and two redeliveries
			if err := pub.Publish(context.Background(), rabbitx.Message{Exchange: rabbitx.ExchangeCommands, RoutingKey: rabbitx.QueueNotificationSend, Publishing: amqp.Publishing{Body: body}}); err != nil {
				t.Fatal(err)
			}
		}
		got := waitMail(t, mp.APIURL, to, 1)
		time.Sleep(2 * time.Second) // give a duplicate time to show up
		got = mailbox(t, mp.APIURL, to)
		if len(got) != 1 || got[0].Subject != "Receipt for invoice INV-2026-000001" {
			t.Fatalf("mailbox = %+v, want exactly one receipt", got)
		}
		// All three deliveries were answered with the same NotificationSent.
		var ids []string
		deadline := time.Now().Add(20 * time.Second)
		for len(ids) < 3 && time.Now().Before(deadline) {
			if d, ok, _ := ch.Get(rabbitx.ReplyQueue, true); ok {
				var r commandsv1.ReplyEnvelope
				_ = proto.Unmarshal(d.Body, &r)
				ids = append(ids, r.GetNotificationSent().GetMessageId())
			} else {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if len(ids) != 3 || ids[0] == "" || ids[0] != ids[1] || ids[1] != ids[2] {
			t.Fatalf("reply message ids = %v, want one id three times", ids)
		}
	})

	t.Run("SandboxMailIsPrefixedAndGoesToTheSandboxSink", func(t *testing.T) {
		sink := testkit.StartMailpit(t)
		m := &delivery.Mailer{Default: mailx.SMTP{Addr: mp.SMTPAddr}, Sandbox: mailx.SMTP{Addr: sink.SMTPAddr}, From: "q@payrudder.local", Notes: notes}
		to := "sandbox-" + uuid.NewString()[:8] + "@example.com"
		cmd := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX,
			Command: &commandsv1.CommandEnvelope_SendNotification{SendNotification: &commandsv1.SendNotification{Template: "payment_failed.v1", To: to,
				Data: map[string]string{"invoice_number": "SBX-2026-000001"}}}}
		if _, err := (&handler.Handler{Mailer: m}).Handle(context.Background(), cmd); err != nil {
			t.Fatal(err)
		}
		got := waitMail(t, sink.APIURL, to, 1)
		if len(got) != 1 || !strings.HasPrefix(got[0].Subject, "[SANDBOX] ") {
			t.Fatalf("sink mailbox = %+v", got)
		}
		if other := mailbox(t, mp.APIURL, to); len(other) != 0 {
			t.Fatalf("a sandbox email reached the default SMTP server: %+v", other)
		}
	})

	t.Run("DisabledCategoryIsDroppedAndTheOthersStillSend", func(t *testing.T) {
		m := &delivery.Mailer{Default: mailx.SMTP{Addr: mp.SMTPAddr}, From: "q@payrudder.local", Notes: notes,
			Switch: offSwitch{"payment_receipts": true}}
		to := "switch-" + uuid.NewString()[:8] + "@example.com"
		send := func(template string) *commandsv1.ReplyEnvelope {
			cmd := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: uuid.NewString(),
				Command: &commandsv1.CommandEnvelope_SendNotification{SendNotification: &commandsv1.SendNotification{Template: template, To: to,
					Data: map[string]string{"invoice_number": "INV-2026-000009"}}}}
			reply, err := (&handler.Handler{Mailer: m}).Handle(context.Background(), cmd)
			if err != nil {
				t.Fatalf("%s: a dropped or sent mail is never an error: %v", template, err)
			}
			return reply
		}
		if r := send("invoice_paid.v1"); !r.GetNotificationSent().GetSuppressed() {
			t.Fatalf("receipts are off, reply = %v", r)
		}
		if r := send("payment_failed.v1"); r.GetNotificationSent().GetSuppressed() || r.GetNotificationSent().GetMessageId() == "" {
			t.Fatalf("payment_failed is on, reply = %v", r)
		}
		time.Sleep(time.Second)
		got := waitMail(t, mp.APIURL, to, 1)
		if len(got) != 1 || got[0].Subject != "Payment failed for invoice INV-2026-000009" {
			t.Fatalf("mailbox = %+v, want only the payment-failed mail", got)
		}
	})

	t.Run("UnsentClaimIsReleasedWhenSMTPIsDown", func(t *testing.T) {
		down := &delivery.Mailer{Default: mailx.SMTP{Addr: "127.0.0.1:1", Timeout: time.Second}, From: "q@payrudder.local", Notes: notes}
		cmd := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(),
			Command: &commandsv1.CommandEnvelope_SendNotification{SendNotification: &commandsv1.SendNotification{Template: "invoice_paid.v1", To: "down@example.com", Data: map[string]string{}}}}
		if _, err := (&handler.Handler{Mailer: down}).Handle(context.Background(), cmd); err == nil {
			t.Fatal("expected an SMTP failure")
		}
		// The retry (SMTP is back) sends at once; it does not wait for the SENDING row to go stale.
		up := &delivery.Mailer{Default: mailx.SMTP{Addr: mp.SMTPAddr}, From: "q@payrudder.local", Notes: store.Notifications{DB: pool, Stale: time.Hour}}
		if _, err := (&handler.Handler{Mailer: up}).Handle(context.Background(), cmd); err != nil {
			t.Fatalf("retry: %v", err)
		}
		if got := waitMail(t, mp.APIURL, "down@example.com", 1); len(got) != 1 {
			t.Fatalf("mailbox = %+v", got)
		}
	})

	t.Run("LimitExceededDeliveredTwiceSendsOneEmail", func(t *testing.T) {
		topic := "usage.limit-exceeded.v1"
		admin, _ := kgo.NewClient(kgo.SeedBrokers(brokers))
		if _, err := kadm.NewClient(admin).CreateTopics(context.Background(), 1, 1, nil, topic, "usage.limit-exceeded.dlq.v1"); err != nil {
			t.Fatal(err)
		}
		admin.Close()
		producer, err := kafkax.NewProducer([]string{brokers})
		if err != nil {
			t.Fatal(err)
		}
		defer producer.Close()

		email := "limit-" + uuid.NewString()[:8] + "@example.com"
		offsets := store.Offsets{DB: pool, Group: "notifier-t", Topic: topic}
		lc := &limits.Consumer{Mailer: mailer, Plans: fakePlans{email: email}, Admin: fakeAdmin{env: plansv1.Environment_ENVIRONMENT_LIVE}, Offsets: offsets,
			DLQ: kafkax.DLQ{Client: producer, Topic: "usage.limit-exceeded.dlq.v1", Consumer: "notifier-t"}, Log: log}
		consumer, err := kafkax.NewConsumer(kafkax.ConsumerConfig{Brokers: []string{brokers}, GroupID: "notifier-t", Topic: topic, LoadOffsets: offsets.Load,
			Handler: lc, MaxPollRecords: 10, Backoff: 100 * time.Millisecond, Log: log})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = consumer.Run(ctx); close(done) }()
		defer func() { cancel(); <-done; consumer.Close() }()

		le, _ := proto.Marshal(&eventsv1.LimitExceeded{EventId: uuid.NewString(), TenantId: uuid.NewString(), CustomerId: uuid.NewString(), MeterCode: "api",
			PeriodId: "2026-10", ThresholdPct: 80, LimitUnits: 10500, UsedUnits: 8400, QuantityScale: 3, DetectedAt: timestamppb.Now()})
		rec := func(v []byte) *kgo.Record { return &kgo.Record{Topic: topic, Key: []byte("tenant"), Value: v} }
		if err := producer.ProduceSync(context.Background(), rec(le), rec(le), rec([]byte{0xff, 0x00})).FirstErr(); err != nil {
			t.Fatal(err)
		}
		got := waitMail(t, mp.APIURL, email, 1)
		time.Sleep(2 * time.Second)
		got = mailbox(t, mp.APIURL, email)
		if len(got) != 1 || got[0].Subject != "You have used 80% of your api limit" {
			t.Fatalf("mailbox = %+v, want exactly one alert", got)
		}
		// The undecodable record went to the DLQ topic with the original bytes.
		cl, _ := kgo.NewClient(kgo.SeedBrokers(brokers), kgo.ConsumeTopics("usage.limit-exceeded.dlq.v1"), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
		defer cl.Close()
		var dead []*kgo.Record
		deadline := time.Now().Add(20 * time.Second)
		for len(dead) == 0 && time.Now().Before(deadline) {
			pctx, pcancel := context.WithTimeout(context.Background(), time.Second)
			cl.PollFetches(pctx).EachRecord(func(r *kgo.Record) { dead = append(dead, r) })
			pcancel()
		}
		if len(dead) != 1 || len(dead[0].Value) != 2 {
			t.Fatalf("dead letters = %v", dead)
		}
		// The offset advanced past all three records.
		testkit.Eventually(t, 10*time.Second, "offset saved", func() bool {
			m, _ := offsets.Load(context.Background(), topic, []int32{0})
			return m[0] == 3
		})
	})
}
