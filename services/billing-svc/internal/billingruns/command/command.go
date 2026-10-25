// Package command drives billing runs: the scheduler that finds due (tenant, period) pairs, the watermark protocol that proves every on-time event is
// aggregated, rating into draft invoices, finalization with gap-free numbers, and the completion check
// (docs/03-services/billing-svc.md#period-close-billing-run). This file is what the operator and the sandbox ask of it; the rules are in the other files.
package command

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
)

// ClosePeriodNow closes a period of a sandbox tenant at once; an empty periodID means the current one in the tenant's time zone.
func (m *Manager) ClosePeriodNow(ctx context.Context, ti tenant.Info, periodID string) (*model.Run, error) {
	if ti.Environment != tenant.EnvSandbox {
		return nil, errkey.New(codes.FailedPrecondition, "billing.run.sandbox_only", "ClosePeriodNow is only available to sandbox tenants")
	}
	pid := periodID
	if pid == "" {
		loc := time.UTC
		if ti.Timezone != "" {
			if l, err := time.LoadLocation(ti.Timezone); err == nil {
				loc = l
			}
		}
		pid = period.PeriodOf(m.Now(), loc)
	}
	if _, _, err := period.Bounds(pid, time.UTC); err != nil {
		return nil, errkey.New(codes.InvalidArgument, "billing.run.bad_period", "period_id must look like 2026-11")
	}
	return m.closePeriodNow(ctx, ti, pid)
}

// notClosable answers for a failed run that could not be retried: the cause goes to the log, the client gets a fixed message.
func notClosable(ctx context.Context, tenantID uuid.UUID, periodID string, err error) error {
	slog.WarnContext(ctx, "failed run not retried", "tenant", tenantID, "period", periodID, "error", err)
	return errkey.New(codes.FailedPrecondition, "billing.run.not_closable", "the run of this period cannot be retried now")
}

// retryLater answers for a run that could not advance: the cause goes to the log, the client gets a fixed message.
func retryLater(ctx context.Context, tenantID uuid.UUID, periodID string, err error) error {
	slog.WarnContext(ctx, "run not driven", "tenant", tenantID, "period", periodID, "error", err)
	return errkey.New(codes.Unavailable, "billing.run.retry_later", "run could not advance now; it will be retried")
}

// ClosePeriod closes a period of a tenant for the operator, retries a failed run and drives it as far as it goes now.
func (m *Manager) ClosePeriod(ctx context.Context, tenantID uuid.UUID, periodID string) (*model.Run, error) {
	run, err := m.closePeriod(ctx, tenantID, periodID)
	var open *model.PeriodOpenError
	switch {
	case errors.Is(err, model.ErrBadPeriod):
		return nil, errkey.New(codes.InvalidArgument, "billing.run.bad_period", "period_id must look like 2026-11")
	case errors.As(err, &open):
		return nil, errkey.Newf(codes.FailedPrecondition, "billing.run.period_open", "period %s closes at %s", open.Period, open.Cutoff.UTC().Format(time.RFC3339))
	case status.Code(err) == codes.NotFound:
		return nil, err // plans-svc's own refusal: no such tenant
	case err != nil:
		return nil, retryLater(ctx, tenantID, periodID, err)
	}
	if run.Status == model.Failed {
		if err := m.RetryRun(ctx, tenantID, periodID); err != nil {
			return nil, notClosable(ctx, tenantID, periodID, err)
		}
	}
	if run, err = m.Drive(ctx, tenantID, periodID); err != nil {
		return nil, retryLater(ctx, tenantID, periodID, err)
	}
	return run, nil
}
