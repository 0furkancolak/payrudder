package runs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Tick runs one scheduler pass: it creates the runs that became due, then drives every unfinished run it can lock.
func (m *Manager) Tick(ctx context.Context) error {
	if err := m.discover(ctx); err != nil {
		m.log.WarnContext(ctx, "scheduler: discovery failed", "error", err)
	}
	if err := m.BillFees(ctx); err != nil {
		m.log.WarnContext(ctx, "scheduler: fee billing failed", "error", err)
	}
	if err := m.SweepRefunds(ctx); err != nil {
		m.log.WarnContext(ctx, "scheduler: credit note refunds not swept", "error", err)
	}
	if err := m.ExpireLinks(ctx); err != nil {
		m.log.WarnContext(ctx, "scheduler: payment links not expired", "error", err)
	}
	if err := m.ResumeRerating(ctx); err != nil {
		m.log.WarnContext(ctx, "scheduler: re-rating resume failed", "error", err)
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
	token := ""
	for {
		resp, err := m.cfg.Admin.ListTenants(operatorCtx(ctx), &plansv1.ListTenantsRequest{PageSize: 500, PageToken: token})
		if err != nil {
			return err
		}
		for _, t := range resp.GetTenants() {
			id, err := uuid.Parse(t.GetId())
			if err != nil {
				continue
			}
			loc, err := time.LoadLocation(t.GetTimezone())
			if err != nil {
				continue
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
		}
		if resp.GetNextPageToken() == "" {
			return nil
		}
		token = resp.GetNextPageToken()
	}
}

// ClosePeriod schedules the close of a period now (operator). It is idempotent per (tenant, period). A live period
// can only be closed after its cutoff.
func (m *Manager) ClosePeriod(ctx context.Context, tenantID uuid.UUID, periodID string) (*Run, error) {
	resp, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tenantID.String()})
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
		return nil, status.Error(codes.InvalidArgument, "period_id must look like 2026-11")
	}
	if existing, err := m.GetRun(ctx, tenantID, periodID); err == nil {
		return existing, nil
	}
	if m.cfg.Now().Before(cutoff) {
		return nil, status.Errorf(codes.FailedPrecondition, "period %s closes at %s", periodID, cutoff.UTC().Format(time.RFC3339))
	}
	return m.EnsureRun(ctx, tenantID, periodID, cutoff, false)
}

// ClosePeriodNow is the sandbox flow: the aggregator lowers the period's cutoff to now + 5 s, and the normal run takes
// over (the watermark check starts WatermarkDelay after the new cutoff). The caller has checked that the tenant is a
// sandbox tenant.
func (m *Manager) ClosePeriodNow(ctx context.Context, ti tenant.Info, periodID string) (*Run, error) {
	tenantID := ti.ID
	resp, err := m.cfg.Aggregator.ClosePeriodEarly(tenant.OutgoingContext(tenant.NewContext(ctx, ti)), &aggregatorv1.ClosePeriodEarlyRequest{PeriodId: periodID})
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

// RunExists reports whether a run exists.
func (m *Manager) RunExists(ctx context.Context, tenantID uuid.UUID, periodID string) (bool, error) {
	_, err := m.GetRun(ctx, tenantID, periodID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
