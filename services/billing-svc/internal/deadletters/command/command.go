// Package command redrives dead letters back to their queues.
package command

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/services/billing-svc/internal/dlq"
)

// Redrive moves up to limit dead letters of queue back and returns how many moved.
func Redrive(ctx context.Context, admin *dlq.Admin, queue string, limit int) (int, error) {
	if admin == nil {
		return 0, status.Error(codes.Unimplemented, "dead-letter operations are not configured")
	}
	n, err := admin.Redrive(ctx, queue, limit)
	if err != nil {
		return 0, brokerErr(err)
	}
	return n, nil
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
