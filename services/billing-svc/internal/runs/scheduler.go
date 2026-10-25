package runs

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	runmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Tick runs one scheduler pass: it creates the runs that became due, then drives every unfinished run it can lock.
func (m *Manager) Tick(ctx context.Context) error {
	if err := m.discover(ctx); err != nil {
		m.log.WarnContext(ctx, "scheduler: discovery failed", "error", err)
	}
	if err := m.ExpireLinks(ctx); err != nil {
		m.log.WarnContext(ctx, "scheduler: payment links not expired", "error", err)
	}
	if err := m.ReversePromotions(ctx); err != nil {
		m.log.WarnContext(ctx, "scheduler: promotion reversals not delivered", "error", err)
	}
	for _, ch := range m.cfg.Chores {
		if err := ch.Run(ctx); err != nil {
			m.log.WarnContext(ctx, "scheduler: "+ch.Name, "error", err)
		}
	}
	open, err := m.q.ListDueRuns(ctx, sqlc.ListDueRunsParams{DelaySecs: m.cfg.WatermarkDelay.Seconds(), Now: m.cfg.Now()})
	if err != nil {
		return err
	}
	for _, k := range open {
		if _, err := m.Drive(ctx, k.TenantID, k.PeriodID); err != nil {
			m.log.WarnContext(ctx, "scheduler: run failed to advance", "tenant", k.TenantID, "period", k.PeriodID, "error", err)
		}
	}
	return nil
}

// discover creates a run for every recently closed period of every tenant that has none yet.
func (m *Manager) discover(ctx context.Context) error {
	now := m.cfg.Now()
	return m.in.EachBillableTenant(ctx, func(id uuid.UUID, t *plansv1.Tenant) error {
		loc, err := time.LoadLocation(t.GetTimezone())
		if err != nil {
			return nil
		}
		created := t.GetCreateTime().AsTime()
		cur := time.Date(now.In(loc).Year(), now.In(loc).Month(), 1, 0, 0, 0, 0, loc)
		for back := 1; back <= m.cfg.Lookback; back++ {
			start := cur.AddDate(0, -back, 0)
			pid := period.PeriodOf(start, loc)
			_, end, err := period.Bounds(pid, loc)
			if err != nil || end.Before(created) {
				continue // the tenant did not exist yet
			}
			cutoff := end.Add(t.GetGracePeriod().AsDuration())
			if now.Before(cutoff.Add(m.cfg.WatermarkDelay)) {
				continue
			}
			if err := m.q.InsertBillingRun(ctx, sqlc.InsertBillingRunParams{TenantID: id, PeriodID: pid, CutoffAt: cutoff}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClosePeriod schedules the close of a period now (operator). It is idempotent per (tenant, period). A live period
// can only be closed after its cutoff.
func (m *Manager) ClosePeriod(ctx context.Context, tenantID uuid.UUID, periodID string) (*runmodel.Run, error) {
	resp, err := m.in.Admin.GetTenant(inputs.OperatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		return nil, err
	}
	t := resp.GetTenant()
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		return nil, err
	}
	cutoff, err := period.Cutoff(periodID, loc, t.GetGracePeriod().AsDuration())
	if err != nil {
		return nil, errkey.New(codes.InvalidArgument, "billing.run.bad_period", "period_id must look like 2026-11")
	}
	if existing, err := m.GetRun(ctx, tenantID, periodID); err == nil {
		return existing, nil
	}
	if m.cfg.Now().Before(cutoff) {
		return nil, errkey.Newf(codes.FailedPrecondition, "billing.run.period_open", "period %s closes at %s", periodID, cutoff.UTC().Format(time.RFC3339))
	}
	return m.EnsureRun(ctx, tenantID, periodID, cutoff, false)
}

// ClosePeriodNow is the sandbox flow: the aggregator lowers the period's cutoff to now + 5 s, and the normal run takes
// over (the watermark check starts WatermarkDelay after the new cutoff). The caller has checked that the tenant is a
// sandbox tenant.
func (m *Manager) ClosePeriodNow(ctx context.Context, ti tenant.Info, periodID string) (*runmodel.Run, error) {
	tenantID := ti.ID
	resp, err := m.in.Aggregator.ClosePeriodEarly(tenant.OutgoingContext(tenant.NewContext(ctx, ti)), &aggregatorv1.ClosePeriodEarlyRequest{PeriodId: periodID})
	if err != nil {
		return nil, err
	}
	cutoff := resp.GetCutoffAt().AsTime()
	run, err := m.EnsureRun(ctx, tenantID, periodID, cutoff, true)
	if err != nil {
		return nil, fmt.Errorf("ensure run: %w", err)
	}
	return run, nil
}
