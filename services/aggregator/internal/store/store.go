// Package store holds what the aggregator modules share of the database: the mapping of a database error to the status a client sees.
// The statements are in queries/ and the sqlc output in sqlc/.
package store

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// MapErr turns an error of a read or a write into the error a client sees: a status passes through, a cancelled context becomes its
// status, and anything else is logged and answered with a fixed "database unavailable".
func MapErr(ctx context.Context, err error) error {
	if _, ok := status.FromError(err); ok && status.Code(err) != codes.Unknown {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return status.FromContextError(err).Err()
	}
	slog.WarnContext(ctx, "aggregator store call failed", "error", err)
	return errkey.New(codes.Unavailable, "aggregator.store.unavailable", "database unavailable")
}
