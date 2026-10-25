// Package query lists dead letters of the broker. It never writes.
package query

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dlq"
)

// List returns the depth of the dead-letter queues and up to limit messages of queue.
func List(ctx context.Context, admin *dlq.Admin, queue string, limit int) ([]dlq.Depth, []dlq.Message, error) {
	if admin == nil {
		return nil, nil, errkey.New(codes.Unimplemented, "billing.dead_letter.not_configured", "dead-letter operations are not configured")
	}
	depths, msgs, err := admin.List(ctx, queue, limit)
	if err != nil {
		return nil, nil, brokerErr(err)
	}
	return depths, msgs, nil
}

// brokerErr maps an error of the dead-letter admin to the one a client sees.
func brokerErr(err error) error {
	switch {
	case errors.Is(err, dlq.ErrUnknownQueue):
		return errkey.New(codes.InvalidArgument, "billing.dead_letter.invalid", err.Error())
	default:
		slog.Error("dead letters", "error", err)
		return errkey.New(codes.Unavailable, "billing.dead_letter.broker_unavailable", "the broker could not be reached; retry")
	}
}
