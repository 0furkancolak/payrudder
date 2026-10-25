// Package query reads re-rating jobs. It never writes.
package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/runs"
)

// Get returns one re-rating job of a tenant.
func Get(ctx context.Context, mgr *runs.Manager, tenantID, jobID uuid.UUID) (*runs.ReratingJob, error) {
	job, err := mgr.GetReratingJob(ctx, tenantID, jobID)
	if errors.Is(err, runs.ErrReratingNotFound) {
		return nil, errkey.New(codes.NotFound, "billing.rerating.not_found", "no such re-rating job")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return job, nil
}
