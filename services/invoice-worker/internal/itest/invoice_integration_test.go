//go:build integration

// Package itest runs invoice-worker against real MinIO and RabbitMQ.
package itest

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/pkg/testkit"
	"github.com/0furkancolak/quotify/pkg/workerx"
	"github.com/0furkancolak/quotify/services/invoice-worker/internal/handler"
)

// countingStore counts the writes that reach the bucket.
type countingStore struct {
	handler.Store
	puts atomic.Int32
}

func (c *countingStore) Put(ctx context.Context, key string, body []byte, o blob.PutOptions) error {
	c.puts.Add(1)
	return c.Store.Put(ctx, key, body, o)
}

func command(tenantID, invoiceID string, env plansv1.Environment, commandID uuid.UUID) []byte {
	b, _ := proto.Marshal(&commandsv1.CommandEnvelope{
		CommandId: commandID.String(), SagaId: uuid.NewString(), TenantId: tenantID, Environment: env,
		Command: &commandsv1.CommandEnvelope_RenderInvoicePdf{RenderInvoicePdf: &commandsv1.RenderInvoicePdf{Invoice: &commandsv1.InvoiceSnapshot{
			InvoiceId: invoiceID, Version: 1, Number: "INV-2026-000001", PeriodId: "2026-10", Currency: "USD", CurrencyExponent: 2,
			TenantName: "Acme", CustomerName: "Ada", CustomerEmail: "ada@example.com", IssuedAt: timestamppb.New(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)),
			Lines:         []*commandsv1.InvoiceLineSnapshot{{LineNo: 1, Kind: "BASE_FEE", Description: "Base fee", AmountMinor: 1000}},
			SubtotalMinor: 1000, TotalMinor: 1000}}}})
	return b
}

func TestInvoiceWorker(t *testing.T) {
	m := testkit.StartMinio(t)
	url := testkit.StartRabbitMQ(t)
	store, err := blob.New(blob.Config{Endpoint: m.Endpoint, Bucket: "invoices", AccessKey: m.AccessKey, SecretKey: m.SecretKey, ForcePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rabbitx.DeclareTopology(url); err != nil {
		t.Fatal(err)
	}
	counting := &countingStore{Store: store}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &workerx.Worker{URL: url, Queues: []string{rabbitx.QueueInvoiceRender}, Handler: &handler.Handler{Store: counting},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	go func() { _ = w.Run(ctx) }()

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, _ := conn.Channel()
	pub := rabbitx.NewPublisher(url)
	defer pub.Close()
	send := func(body []byte) {
		t.Helper()
		var err error
		for i := 0; i < 50; i++ {
			if err = pub.Publish(context.Background(), rabbitx.Message{Exchange: rabbitx.ExchangeCommands, RoutingKey: rabbitx.QueueInvoiceRender, Publishing: amqp.Publishing{Body: body}}); err == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal(err)
	}
	replies := func(n int) []*commandsv1.ReplyEnvelope {
		t.Helper()

		var out []*commandsv1.ReplyEnvelope
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) && len(out) < n {
			d, ok, _ := ch.Get(rabbitx.ReplyQueue, true)
			if !ok {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			var r commandsv1.ReplyEnvelope
			_ = proto.Unmarshal(d.Body, &r)
			out = append(out, &r)
		}
		return out
	}

	tenantID, invoiceID := uuid.NewString(), uuid.NewString()
	t.Run("RedeliveredCommandRendersOnce", func(t *testing.T) {
		send(command(tenantID, invoiceID, plansv1.Environment_ENVIRONMENT_SANDBOX, uuid.New()))
		send(command(tenantID, invoiceID, plansv1.Environment_ENVIRONMENT_SANDBOX, uuid.New())) // another attempt of the same step (fresh command id)
		got := replies(2)
		if len(got) != 2 {
			t.Fatalf("%d replies, want 2", len(got))
		}
		a, b := got[0].GetPdfRendered(), got[1].GetPdfRendered()
		if a == nil || b == nil || a.GetObjectKey() != b.GetObjectKey() || a.GetSha256() != b.GetSha256() || a.GetSha256() == "" {
			t.Fatalf("replies = %v %v, want the same object and hash twice", a, b)
		}
		if a.GetObjectKey() != tenantID+"/"+invoiceID+"/v1.pdf" {
			t.Fatalf("object key = %s", a.GetObjectKey())
		}
		if n, err := store.Count(context.Background(), tenantID+"/"+invoiceID+"/"); err != nil || n != 1 {
			t.Fatalf("%d objects in the bucket, %v; want 1", n, err)
		}
		if counting.puts.Load() != 1 {
			t.Fatalf("%d writes, want 1 (the second command must not re-render)", counting.puts.Load())
		}
		info, err := store.Head(context.Background(), a.GetObjectKey())
		if err != nil || info.Metadata["sha256"] != a.GetSha256() || info.ContentType != "application/pdf" || info.Size != a.GetSizeBytes() {
			t.Fatalf("object = %+v, %v", info, err)
		}
		tags, _ := store.Tags(context.Background(), a.GetObjectKey())
		if tags["environment"] != "sandbox" {
			t.Fatalf("tags = %v, want environment=sandbox for the lifecycle rule", tags)
		}
		pdf, _ := store.Get(context.Background(), a.GetObjectKey())
		if len(pdf) < 5 || string(pdf[:5]) != "%PDF-" {
			t.Fatal("the stored object is not a PDF")
		}
	})

	t.Run("LiveInvoiceIsTaggedLive", func(t *testing.T) {
		send(command(tenantID, uuid.NewString(), plansv1.Environment_ENVIRONMENT_LIVE, uuid.New()))
		got := replies(1)
		if len(got) != 1 || got[0].GetPdfRendered() == nil {
			t.Fatalf("replies = %v", got)
		}
		tags, _ := store.Tags(context.Background(), got[0].GetPdfRendered().GetObjectKey())
		if tags["environment"] != "live" {
			t.Fatalf("tags = %v", tags)
		}
	})

	t.Run("InvalidSnapshotIsAPermanentFailureReply", func(t *testing.T) {
		b := command(tenantID, uuid.NewString(), plansv1.Environment_ENVIRONMENT_LIVE, uuid.New())
		var cmd commandsv1.CommandEnvelope
		_ = proto.Unmarshal(b, &cmd)
		cmd.GetRenderInvoicePdf().Invoice.Number = ""
		bad, _ := proto.Marshal(&cmd)
		send(bad)
		got := replies(1)
		if len(got) != 1 || got[0].GetPdfFailed() == nil {
			t.Fatalf("replies = %v, want PdfFailed", got)
		}
	})
}
