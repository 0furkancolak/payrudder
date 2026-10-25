// Package apierr is how billing-svc answers a failure it did not expect: the cause goes to the log, the client gets a fixed Internal error. The errors a module
// returns on purpose carry a key and a code of their own (pkg/errkey) and pass through Wrap untouched.
package apierr

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Internal logs err and answers with Internal and a fixed message: the cause (a database error, an upstream message) is for the operators, never the client.
// A request the client abandoned is logged as a warning, not an error.
func Internal(ctx context.Context, err error) error {
	level := slog.LevelError
	if ctx.Err() != nil {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "request failed", "error", err)
	return status.Error(codes.Internal, "internal error")
}

// Wrap passes on an error that already has a gRPC status of its own (the refusals a module made on purpose, with their keys) and answers anything else with Internal.
func Wrap(ctx context.Context, err error) error {
	if _, ok := status.FromError(err); ok && status.Code(err) != codes.Unknown {
		return err
	}
	return Internal(ctx, err)
}
