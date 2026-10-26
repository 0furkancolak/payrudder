// Package query reads the caller's tenant: its settings and its time zone.
package query

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Current returns the caller's tenant row. The tenant must exist (the gateway resolved it from an API key).
func Current(ctx context.Context, q *sqlc.Queries) (sqlc.CurrentTenantRow, error) {
	t, err := q.CurrentTenant(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, errkey.New(codes.Unauthenticated, "plans.tenant.unknown", "unknown tenant")
	}
	return t, err
}

// Location loads the tenant's time zone.
func Location(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "tenant time zone %q: %v", name, err)
	}
	return loc, nil
}

// CurrentIn is Current with the tenant's time zone loaded: what a change of a subscription or a fee needs to find the billing periods in the tenant's days.
func CurrentIn(ctx context.Context, q *sqlc.Queries) (sqlc.CurrentTenantRow, *time.Location, error) {
	t, err := Current(ctx, q)
	if err != nil {
		return t, nil, err
	}
	loc, err := Location(t.Timezone)
	return t, loc, err
}
