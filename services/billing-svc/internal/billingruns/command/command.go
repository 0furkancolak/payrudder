// Package command closes billing periods: the sandbox clock and the operator's close. The rules live here.
package command

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
)

// ClosePeriodNow closes a period of a sandbox tenant at once; an empty periodID means the current one in the tenant's time zone.
func ClosePeriodNow(ctx context.Context, mgr *runs.Manager, ti tenant.Info, periodID string) (*runs.Run, error) {
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
		pid = period.PeriodOf(mgr.Now(), loc)
	}
	if _, _, err := period.Bounds(pid, time.UTC); err != nil {
		return nil, errkey.New(codes.InvalidArgument, "billing.run.bad_period", "period_id must look like 2026-11")
	}
	return mgr.ClosePeriodNow(ctx, ti, pid)
}

// ClosePeriod closes a period of a tenant for the operator, retries a failed run and drives it as far as it goes now.
func ClosePeriod(ctx context.Context, mgr *runs.Manager, tenantID uuid.UUID, periodID string) (*runs.Run, error) {
	run, err := mgr.ClosePeriod(ctx, tenantID, periodID)
	if err != nil {
		return nil, err
	}
	if run.Status == runs.Failed {
		if err := mgr.RetryRun(ctx, tenantID, periodID); err != nil {
			return nil, errkey.New(codes.FailedPrecondition, "billing.run.not_closable", err.Error())
		}
	}
	if run, err = mgr.Drive(ctx, tenantID, periodID); err != nil {
		return nil, errkey.New(codes.Unavailable, "billing.run.retry_later", "run could not advance now; it will be retried: "+err.Error())
	}
	return run, nil
}
