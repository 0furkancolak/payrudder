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
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Settings are the retry delays of a tenant. UpdatedAt is nil while the service-wide defaults are in use.
type Settings struct {
	RetryHours    []int32
	UpdatedAt     *time.Time
	DefaultsInUse bool
}

// Defaults gives the delays the service uses for a tenant that set none (sagas/command's engine, with the configuration it runs on).
type Defaults interface {
	DunningDefaults(sandbox bool) []time.Duration
}

// Get returns the tenant's dunning settings, or the service-wide defaults when it has none.
func Get(ctx context.Context, app *pgxpool.Pool, defaults Defaults, ti tenant.Info) (*Settings, error) {
	out := &Settings{}
	sandbox := ti.Environment == env.Sandbox.String()
	err := pgxx.InTenantTx(ctx, app, ti.ID, func(tx pgx.Tx) error {
		row, err := sqlc.New(tx).GetDunningSettings(ctx, ti.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			out.DefaultsInUse = true
			out.RetryHours = retryHours(defaults.DunningDefaults(sandbox), sandbox)
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

// retryHours shows delays the way a tenant would set them: hours, or minutes in a sandbox tenant.
func retryHours(delays []time.Duration, sandbox bool) []int32 {
	unit := time.Hour
	if sandbox {
		unit = time.Minute
	}
	out := make([]int32, len(delays))
	for i, v := range delays {
		out[i] = int32(v / unit) //nolint:gosec // a delay is a few days at most
	}
	return out
}
