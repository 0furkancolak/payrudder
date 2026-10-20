// Package command starts re-rating jobs for the operator.
package command

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
)

// Start starts, or finds, the re-rating job of a period under a plan version. created says whether it is new.
// A job that exists but could not be driven now is returned without an error: the scheduler finishes it.
func Start(ctx context.Context, mgr *runs.Manager, tenantID uuid.UUID, periodID string, planVersionID uuid.UUID) (job *runs.ReratingJob, created bool, err error) {
	job, created, err = mgr.StartRerating(ctx, tenantID, periodID, planVersionID)
	switch {
	case errors.Is(err, runs.ErrNoRun):
		return nil, false, status.Error(codes.NotFound, "no billing run for that period")
	case errors.Is(err, runs.ErrRunNotCompleted):
		return nil, false, status.Error(codes.FailedPrecondition, "the billing run of that period is not COMPLETED")
	case errors.Is(err, runs.ErrUnknownPlanVer):
		return nil, false, status.Error(codes.InvalidArgument, "plan_version_id: no such plan version")
	case err != nil && job != nil:
		return job, created, nil
	case err != nil:
		slog.ErrorContext(ctx, "start rerating", "error", err)
		return nil, false, status.Error(codes.Unavailable, "re-rating could not start; retry")
	}
	return job, created, nil
}
