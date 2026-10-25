// Package query lists dead letters of the broker. It never writes: it takes a message off its queue to read it and puts it back unacknowledged, which the broker
// shows as a redelivery of that message.
package query

import (
	"context"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/deadletters/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dlq"
)

// List returns the depth of the dead-letter queues and up to limit messages of queue.
func List(ctx context.Context, admin *dlq.Admin, queue string, limit int) ([]dlq.Depth, []dlq.Message, error) {
	if admin == nil {
		return nil, nil, model.ErrNotConfigured
	}
	depths, msgs, err := admin.List(ctx, queue, limit)
	if err != nil {
		return nil, nil, model.Refusal(ctx, err)
	}
	return depths, msgs, nil
}
