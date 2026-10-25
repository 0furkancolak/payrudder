// Package model is what the dead-letter module answers when the broker or the request is the problem.
package model

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dlq"
)

// ErrNotConfigured is the answer when the service runs without a broker connection for dead-letter operations.
var ErrNotConfigured = errkey.New(codes.Unimplemented, "billing.dead_letter.not_configured", "dead-letter operations are not configured")

// Refusal maps an error of the dead-letter admin to the one a client sees: an unknown queue is the request's fault, anything else is the broker's and is logged.
func Refusal(ctx context.Context, err error) error {
	if errors.Is(err, dlq.ErrUnknownQueue) {
		return errkey.New(codes.InvalidArgument, "billing.dead_letter.invalid", err.Error())
	}
	slog.ErrorContext(ctx, "dead letters", "error", err)
	return errkey.New(codes.Unavailable, "billing.dead_letter.broker_unavailable", "the broker could not be reached; retry")
}
