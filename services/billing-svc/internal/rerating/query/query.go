// Package query reads re-rating jobs. It never writes.
package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Load reads one job; the error is model.ErrNotFound when there is none, otherwise the database's.
func Load(ctx context.Context, q *sqlc.Queries, tenantID, jobID uuid.UUID) (*model.Job, error) {
	row, err := q.GetReratingJob(ctx, sqlc.GetReratingJobParams{TenantID: tenantID, ID: jobID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return model.Of(row), nil
}

// Get returns one re-rating job of a tenant.
func Get(ctx context.Context, q *sqlc.Queries, tenantID, jobID uuid.UUID) (*model.Job, error) {
	job, err := Load(ctx, q, tenantID, jobID)
	if errors.Is(err, model.ErrNotFound) {
		return nil, errkey.New(codes.NotFound, "billing.rerating.not_found", "no such re-rating job")
	}
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return job, nil
}
