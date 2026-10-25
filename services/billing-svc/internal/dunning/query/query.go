// Package query reads the dunning settings of a tenant. It never writes.
package query

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/saga"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Settings are the retry delays of a tenant. UpdatedAt is nil while the service-wide defaults are in use.
type Settings struct {
	RetryHours    []int32
	UpdatedAt     *time.Time
	DefaultsInUse bool
}

// Get returns the tenant's dunning settings, or the service-wide defaults when it has none.
func Get(ctx context.Context, app *pgxpool.Pool, ti tenant.Info) (*Settings, error) {
	out := &Settings{}
	err := pgxx.InTenantTx(ctx, app, ti.ID, func(tx pgx.Tx) error {
		row, err := sqlc.New(tx).GetDunningSettings(ctx, ti.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			out.DefaultsInUse = true
			out.RetryHours = defaultRetryHours(ti.Environment == env.Sandbox.String())
			return nil
		}
		if err != nil {
			return err
		}
		out.RetryHours, out.UpdatedAt = row.RetryHours, &row.UpdatedAt
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return out, nil
}

// defaultRetryHours shows the service-wide delays the way a tenant would set them: hours, or minutes in a sandbox tenant.
func defaultRetryHours(sandbox bool) []int32 {
	d, unit := saga.DefaultDunningLive, time.Hour
	if sandbox {
		d, unit = saga.DefaultDunningSandbox, time.Minute
	}
	out := make([]int32, len(d))
	for i, v := range d {
		out[i] = int32(v / unit) //nolint:gosec // the defaults are small
	}
	return out
}
