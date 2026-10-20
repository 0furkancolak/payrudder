// Package query lists dead letters of the broker. It never writes.
package query

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/services/billing-svc/internal/dlq"
)

// List returns the depth of the dead-letter queues and up to limit messages of queue.
func List(ctx context.Context, admin *dlq.Admin, queue string, limit int) ([]dlq.Depth, []dlq.Message, error) {
	if admin == nil {
		return nil, nil, status.Error(codes.Unimplemented, "dead-letter operations are not configured")
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
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		slog.Error("dead letters", "error", err)
		return status.Error(codes.Unavailable, "the broker could not be reached; retry")
	}
}
