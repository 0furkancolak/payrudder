// Package command redrives dead letters back to their queues.
package command

import (
	"context"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/deadletters/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dlq"
)

// Redrive moves up to limit dead letters of queue back and returns how many moved.
func Redrive(ctx context.Context, admin *dlq.Admin, queue string, limit int) (int, error) {
	if admin == nil {
		return 0, model.ErrNotConfigured
	}
	n, err := admin.Redrive(ctx, queue, limit)
	if err != nil {
		return 0, model.Refusal(ctx, err)
	}
	return n, nil
}
