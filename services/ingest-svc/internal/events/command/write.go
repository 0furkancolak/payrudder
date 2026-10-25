package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
)

// write persists the batch in one transaction and fills results for every pending event.
func (c *Commands) write(ctx context.Context, ti tenant.Info, todo []*pending, results []*ingestv1.EventResult) error {
	traceHeaders := c.traceHeaders(ctx)
	err := pgxx.InTenantTx(ctx, c.o.App, ti.ID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.SetTransactionTimeout(ctx, writeTimeout); err != nil {
			return fmt.Errorf("set transaction_timeout: %w", err)
		}
		now, err := c.o.NowInTx(ctx, q)
		if err != nil {
			return fmt.Errorf("clock: %w", err)
		}
		bucket := domain.BucketOf(now)
		earlier := []time.Time{bucket.AddDate(0, -1, 0), bucket.AddDate(0, -2, 0)} // MaxLateness spans up to three months

		accepted, err := c.dedup(ctx, q, ti.ID, bucket, earlier, todo, results)
		if err != nil {
			return err
		}
		if len(accepted) == 0 {
			return nil
		}
		payloads, headers, err := outboxRows(accepted, now, ti.ID.String(), traceHeaders)
		if err != nil {
			return err
		}
		if err := q.EnqueueOutbox(ctx, sqlc.EnqueueOutboxParams{
			TenantID: ti.ID, Topic: Topic, PartitionKey: ti.ID.String(),
			Payloads: payloads, Headers: headers, IngestedAt: now,
		}); err != nil {
			return fmt.Errorf("enqueue outbox: %w", err)
		}
		for _, p := range accepted {
			results[p.idx] = &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_ACCEPTED, EventId: p.event.EventId}
		}
		return nil
	})
	if err != nil {
		return c.mapDBError(ctx, err)
	}
	return nil
}

// traceHeaders are the headers every outbox row of the request carries: the trace context and the debug mark.
func (c *Commands) traceHeaders(ctx context.Context) map[string]string {
	h := map[string]string{}
	kafkax.InjectTrace(ctx, h, c.o.Propagator)
	// A debug-marked request (sandbox tenants and operators may ask for it) gets a span in every consumer.
	if v := metadata.ValueFromIncomingContext(ctx, kafkax.DebugHeader); len(v) > 0 && v[0] == "1" {
		h[kafkax.DebugHeader] = "1"
	}
	return h
}

// outboxRows stamps the events with the ingest time and builds the payload and header JSON of every outbox row.
func outboxRows(accepted []*pending, now time.Time, tenantID string, traceHeaders map[string]string) (payloads [][]byte, headers []string, err error) {
	payloads = make([][]byte, len(accepted))
	headers = make([]string, len(accepted))
	for i, p := range accepted {
		p.event.IngestedAt = timestamppb.New(now)
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(p.event)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal event: %w", err)
		}
		payloads[i] = b
		h := kafkax.Standard(ceType, p.event.EventId, tenantID)
		for k, v := range traceHeaders {
			h[k] = v
		}
		hb, err := json.Marshal(h)
		if err != nil {
			return nil, nil, err
		}
		headers[i] = string(hb)
	}
	return payloads, headers, nil
}

// SQLSTATE classes the write transaction can fail with.
const (
	sqlQueryCanceled       = "57014" // transaction_timeout
	sqlIdleInTxTimeout     = "25P03" // idle_in_transaction_session_timeout
	sqlSerializationFailed = "40001"
	sqlDeadlockDetected    = "40P01"
)

func (c *Commands) mapDBError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return errkey.New(codes.DeadlineExceeded, "ingest.db.timeout", "ingest timed out; nothing was accepted, retry is safe")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "canceled")
	}
	switch pgxx.ErrCode(err) {
	case sqlQueryCanceled, sqlIdleInTxTimeout:
		return errkey.New(codes.Unavailable, "ingest.db.slow", "database too slow; nothing was accepted, retry is safe")
	case sqlSerializationFailed, sqlDeadlockDetected:
		return errkey.New(codes.Aborted, "ingest.db.conflict", "conflict; retry is safe")
	}
	c.log.ErrorContext(ctx, "ingest: write failed", "error", err)
	return errkey.New(codes.Unavailable, "ingest.db.unavailable", "database unavailable; nothing was accepted, retry is safe")
}
