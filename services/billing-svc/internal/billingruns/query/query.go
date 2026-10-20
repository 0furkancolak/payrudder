// Package query reads billing runs. It never writes.
package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
)

// Get returns the billing run of a tenant's period.
func Get(ctx context.Context, mgr *runs.Manager, tenantID uuid.UUID, periodID string) (*runs.Run, error) {
	run, err := mgr.GetRun(ctx, tenantID, periodID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "no billing run for that period")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return run, nil
}
