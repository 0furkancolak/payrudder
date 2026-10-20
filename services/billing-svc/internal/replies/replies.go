// Package replies consumes the workers' replies from billing-svc.replies and feeds them to the saga.
package replies

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Consumer applies replies to sagas.
type Consumer struct {
	Pool   *pgxpool.Pool // billing_system
	Engine *saga.Engine
	URL    string
	Log    *slog.Logger

	received   metric.Int64Counter
	duplicates metric.Int64Counter
}

// New registers metrics.
func New(pool *pgxpool.Pool, eng *saga.Engine, url string, log *slog.Logger) (*Consumer, error) {
	if log == nil {
		log = slog.Default()
	}
	m := otel.Meter("github.com/0furkancolak/quotify/services/billing-svc/replies")
	c := &Consumer{Pool: pool, Engine: eng, URL: url, Log: log}
	var err error
	if c.received, err = m.Int64Counter("billing_replies_received", metric.WithDescription("Worker replies handled, by kind and whether they changed a saga")); err != nil {
		return nil, err
	}
	if c.duplicates, err = m.Int64Counter("billing_replies_duplicate", metric.WithDescription("Replies dropped by the inbox (same reply id seen before)")); err != nil {
		return nil, err
	}
	return c, nil
}

// Run consumes billing-svc.replies until ctx is done.
func (c *Consumer) Run(ctx context.Context) error {
	return rabbitx.Consume(ctx, c.URL, rabbitx.ReplyQueue, 16, c.Handle, c.Log)
}

// Handle processes one delivery: decode, dedup on the reply id, apply to the saga. A database error requeues the
// reply (the quorum queue's delivery limit dead-letters a poison one); an undecodable one is dead-lettered.
func (c *Consumer) Handle(ctx context.Context, d amqp.Delivery) rabbitx.Outcome {
	var reply commandsv1.ReplyEnvelope
	if err := proto.Unmarshal(d.Body, &reply); err != nil {
		c.Log.WarnContext(ctx, "undecodable reply", "error", err)
		return rabbitx.Reject
	}
	ev, cause, ok := saga.ReplyEvent(&reply)
	sagaID, err1 := uuid.Parse(reply.GetSagaId())
	tenantID, err2 := uuid.Parse(reply.GetTenantId())
	replyID, err3 := uuid.Parse(reply.GetReplyId())
	cmdID, err4 := uuid.Parse(reply.GetCommandId())
	if !ok || err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		c.Log.WarnContext(ctx, "malformed reply", "saga", reply.GetSagaId())
		return rabbitx.Reject
	}
	kind := string(ev)
	var applied bool
	err := pgxx.InTx(ctx, c.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		fresh, err := q.InsertInboxReply(ctx, replyID)
		if err != nil {
			return err
		}
		if fresh == 0 {
			c.duplicates.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind)))
			return nil
		}
		// A refund of a credit note is answered like any command, but no saga is waiting for it.
		if handled, err := runs.SettleRefund(ctx, tx, tenantID, cmdID, &reply); err != nil || handled {
			return err
		}
		out, err := c.Engine.Handle(ctx, tx, tenantID, sagaID, ev, cause, &cmdID)
		if err != nil {
			return err
		}
		applied = out.Applied
		if out.Applied && ev == saga.EvPdfRendered {
			err = q.SetInvoicePDFKeyBySaga(ctx, sqlc.SetInvoicePDFKeyBySagaParams{PdfObjectKey: reply.GetPdfRendered().GetObjectKey(), TenantID: tenantID, SagaID: sagaID})
		}
		return err
	})
	if err != nil {
		if errors.Is(err, saga.ErrNotFound) {
			c.Log.WarnContext(ctx, "reply for an unknown saga", "saga", sagaID)
			return rabbitx.Reject
		}
		c.Log.WarnContext(ctx, "reply not applied, will be redelivered", "error", err)
		return rabbitx.Requeue
	}
	c.received.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind), attribute.Bool("applied", applied)))
	return rabbitx.Ack
}
