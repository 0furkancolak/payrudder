// Package command changes the state of a billing period: it freezes it, or closes it early in a sandbox.
package command

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/settings"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// Commands changes periods on the app pool (RLS enforced).
type Commands struct {
	app      *pgxpool.Pool
	settings *settings.Source
}

// New returns the period commands.
func New(app *pgxpool.Pool, src *settings.Source) *Commands {
	return &Commands{app: app, settings: src}
}

func invalid(key, msg string) error { return errkey.New(codes.InvalidArgument, key, msg) }

func validPeriod(p string) error {
	if !period.Valid(p) {
		return invalid("aggregator.period.bad_id", "period_id must look like 2026-11")
	}
	return nil
}

// cannotCompute answers for a period whose settings could not be worked out: the cause goes to the log, the client gets a fixed message.
func cannotCompute(ctx context.Context, tenantID uuid.UUID, periodID string, err error) error {
	slog.WarnContext(ctx, "period settings not computed", "tenant", tenantID, "period", periodID, "error", err)
	return invalid("aggregator.period.cannot_compute", "the settings of this period cannot be worked out")
}

// ensurePeriod makes sure the tenant's period has a settings row (a period without events has none yet).
func (c *Commands) ensurePeriod(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, periodID string) error {
	t, err := c.settings.Tenant(ctx, tenantID)
	if err != nil {
		return err
	}
	snap, err := settings.Compute(t, periodID)
	if err != nil {
		return cannotCompute(ctx, tenantID, periodID, err)
	}
	return sqlc.New(tx).EnsurePeriodSettingsApp(ctx, sqlc.EnsurePeriodSettingsAppParams{
		PeriodID: periodID, Timezone: snap.Timezone, PeriodStart: snap.Start, PeriodEnd: snap.End,
		GraceSeconds: snap.GraceSeconds, CutoffAt: snap.Cutoff,
	})
}

// Freeze freezes the period of the calling tenant and returns when it was frozen (nil when there was nothing to freeze).
func (c *Commands) Freeze(ctx context.Context, periodID string) (*time.Time, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := validPeriod(periodID); err != nil {
		return nil, err
	}
	var frozen *time.Time
	err = pgxx.InTenantTx(ctx, c.app, ti.ID, func(tx pgx.Tx) error {
		if err := c.ensurePeriod(ctx, tx, ti.ID, periodID); err != nil {
			return err
		}
		at, err := sqlc.New(tx).FreezePeriod(ctx, periodID)
		frozen = at
		return err
	})
	if err != nil {
		return nil, store.MapErr(ctx, err)
	}
	return frozen, nil
}

// CloseEarly brings the cutoff of the period of a sandbox tenant to within seconds and returns the new cutoff.
func (c *Commands) CloseEarly(ctx context.Context, periodID string) (time.Time, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return time.Time{}, err
	}
	// The environment comes from the authenticated tenant, never from the request.
	if ti.Environment != tenant.EnvSandbox {
		return time.Time{}, errkey.New(codes.FailedPrecondition, "aggregator.period.not_sandbox", "periods can only be closed early in a sandbox tenant")
	}
	if err := validPeriod(periodID); err != nil {
		return time.Time{}, err
	}
	var cutoff time.Time
	err = pgxx.InTenantTx(ctx, c.app, ti.ID, func(tx pgx.Tx) error {
		if err := c.ensurePeriod(ctx, tx, ti.ID, periodID); err != nil {
			return err
		}
		at, err := sqlc.New(tx).ClosePeriodEarly(ctx, periodID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errkey.New(codes.FailedPrecondition, "aggregator.period.frozen", "period is frozen or already closes within 5 seconds")
		}
		cutoff = at
		return err
	})
	if err != nil {
		return time.Time{}, store.MapErr(ctx, err)
	}
	return cutoff, nil
}
