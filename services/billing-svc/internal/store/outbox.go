package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"

	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// EnqueueOutbox inserts one outbox row (a no-op when the message id exists) and signals the relay.
func EnqueueOutbox(ctx context.Context, tx pgx.Tx, tenantID, messageID uuid.UUID, topic, key, dest string, payload []byte) error {
	h := map[string]string{outbox.DestinationHeader: dest, outbox.MessageIDHeader: messageID.String()}
	// The relay and every consumer continue the trace of the transition that produced the message.
	kafkax.InjectTrace(ctx, h, otel.GetTextMapPropagator())
	hdr, _ := json.Marshal(h)
	q := sqlc.New(tx)
	if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{TenantID: tenantID, Topic: topic, PartitionKey: key, Payload: payload, Headers: hdr, MessageID: messageID}); err != nil {
		return err
	}
	return q.NotifyOutbox(ctx, outbox.NotifyChannel)
}
