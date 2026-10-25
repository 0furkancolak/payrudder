// Package command records the events a tenant can subscribe to, in the caller's tenant transaction, and queues their deliveries. The worker package
// delivers them.
package command

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// Emit records the event and queues a delivery for each active endpoint that wants its type. It is a no-op when no endpoint would receive it. A repeated
// event id is ignored. It returns how many deliveries were queued.
func Emit(ctx context.Context, q *sqlc.Queries, id uuid.UUID, typ string, data json.RawMessage, at time.Time) (int64, error) {
	wanted, err := q.HasWebhookTargets(ctx, typ)
	if err != nil || !wanted {
		return 0, err
	}
	return queue(ctx, q, id, typ, data, at)
}

// queue records an event somebody wants and queues its deliveries.
func queue(ctx context.Context, q *sqlc.Queries, id uuid.UUID, typ string, data json.RawMessage, at time.Time) (int64, error) {
	n, err := q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: id, Type: typ, Data: data, OccurredAt: at})
	if err != nil || n == 0 {
		return 0, err
	}
	return q.QueueWebhookDeliveries(ctx, sqlc.QueueWebhookDeliveriesParams{EventID: id, EventType: typ})
}

// EmitSubscription records a subscription event: {"subscription": <as the API returns it>} plus extra fields.
func EmitSubscription(ctx context.Context, q *sqlc.Queries, now time.Time, typ string, sub *plansv1.Subscription, extra map[string]any) error {
	wanted, err := q.HasWebhookTargets(ctx, typ)
	if err != nil || !wanted {
		return err
	}
	raw, err := model.EventJSON.Marshal(sub)
	if err != nil {
		return err
	}
	data := map[string]any{"subscription": json.RawMessage(raw)}
	for k, v := range extra {
		data[k] = v
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = queue(ctx, q, idgen.NewUUIDv7(), typ, b, now)
	return err
}
