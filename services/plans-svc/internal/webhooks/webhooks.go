// Package webhooks records the events a tenant can subscribe to and delivers them, signed, to the tenant's endpoints.
package webhooks

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Event types a tenant can subscribe an endpoint to. An endpoint with no types gets all of them.
const (
	Ping                        = "ping"
	InvoiceFinalized            = "invoice.finalized"
	InvoicePaid                 = "invoice.paid"
	InvoiceVoided               = "invoice.voided"
	InvoiceUncollectible        = "invoice.uncollectible"
	CreditNoteCreated           = "credit_note.created"
	CreditNoteRefunded          = "credit_note.refunded"
	CreditNoteRefundFailed      = "credit_note.refund_failed"
	SubscriptionCreated         = "subscription.created"
	SubscriptionChanged         = "subscription.changed"
	SubscriptionChangeScheduled = "subscription.change_scheduled"
	SubscriptionCancelled       = "subscription.cancelled"
	SubscriptionCancelScheduled = "subscription.cancellation_scheduled"
	SubscriptionCancelWithdrawn = "subscription.cancellation_withdrawn"
	SubscriptionPaused          = "subscription.paused"
	SubscriptionResumed         = "subscription.resumed"
	SubscriptionMovedToProvider = "subscription.moved_to_provider"
)

// Types lists every event type, in the order the documentation gives them.
var Types = []string{
	InvoiceFinalized, InvoicePaid, InvoiceVoided, InvoiceUncollectible,
	CreditNoteCreated, CreditNoteRefunded, CreditNoteRefundFailed,
	SubscriptionCreated, SubscriptionChanged, SubscriptionChangeScheduled, SubscriptionCancelled, SubscriptionCancelScheduled, SubscriptionCancelWithdrawn,
	SubscriptionPaused, SubscriptionResumed, SubscriptionMovedToProvider,
}

// Known reports whether t is an event type (ping is sent only on request and cannot be subscribed to).
func Known(t string) bool {
	for _, k := range Types {
		if k == t {
			return true
		}
	}
	return false
}

// Emit records the event and queues a delivery for each active endpoint that wants its type, in the caller's tenant transaction. It is a no-op when no
// endpoint would receive it. A repeated event id is ignored. It returns how many deliveries were queued.
func Emit(ctx context.Context, q *sqlc.Queries, id uuid.UUID, typ string, data json.RawMessage, at time.Time) (int64, error) {
	any, err := q.HasWebhookTargets(ctx, typ)
	if err != nil || !any {
		return 0, err
	}
	n, err := q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: id, Type: typ, Data: data, OccurredAt: at})
	if err != nil || n == 0 {
		return 0, err
	}
	return q.QueueWebhookDeliveries(ctx, sqlc.QueueWebhookDeliveriesParams{EventID: id, EventType: typ})
}
