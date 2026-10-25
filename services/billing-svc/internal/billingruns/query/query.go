// Package query reads billing runs. It never writes.
package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Load reads the run of a tenant's period. The error is the database's: pgx.ErrNoRows when there is none.
func Load(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, periodID string) (*model.Run, error) {
	row, err := q.GetBillingRun(ctx, sqlc.GetBillingRunParams{TenantID: tenantID, PeriodID: periodID})
	if err != nil {
		return nil, err
	}
	return model.Of(row)
}

// Runs is what Get reads runs through.
type Runs interface {
	GetRun(ctx context.Context, tenantID uuid.UUID, periodID string) (*model.Run, error)
}

// Get returns the billing run of a tenant's period.
func Get(ctx context.Context, runs Runs, tenantID uuid.UUID, periodID string) (*model.Run, error) {
	run, err := runs.GetRun(ctx, tenantID, periodID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.NotFound, "billing.run.not_found", "no billing run for that period")
	}
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return run, nil
}
