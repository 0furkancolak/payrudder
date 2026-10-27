package command

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// Counts is how many runs sit in each status and how many sagas in each state. The counts add up over every place a tenant's rows can be (the pooled database, each
// tenant placed in a cell, ADR 0018); what could be counted is returned with the error of what could not.
func (m *Manager) Counts(ctx context.Context) (runs, sagas map[string]int64, err error) {
	runs, sagas = map[string]int64{}, map[string]int64{}
	err = m.cfg.DB.Each(ctx, func(ctx context.Context, s tenantdb.Scope) error {
		byStatus, err := s.Scan().CountRunsByStatus(ctx)
		if err != nil {
			return err
		}
		for _, r := range byStatus {
			runs[r.Status] += r.Runs
		}
		byState, err := s.Scan().CountSagasByState(ctx)
		if err != nil {
			return err
		}
		for _, r := range byState {
			sagas[r.State] += r.Sagas
		}
		return nil
	})
	return runs, sagas, err
}

// registerGauges exposes how many runs sit in each status and how many sagas in each state, so stuck work is visible
// without a query (docs/08-observability.md: billing_runs, saga_state). Both read the system pool on scrape.
func (m *Manager) registerGauges(meter metric.Meter) error {
	runs, err := meter.Int64ObservableGauge("billing_runs", metric.WithDescription("Billing runs by status"))
	if err != nil {
		return err
	}
	sagas, err := meter.Int64ObservableGauge("saga_state", metric.WithDescription("Invoice sagas by state"))
	if err != nil {
		return err
	}
	_, err = meter.RegisterCallback(func(ctx context.Context, ob metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		runCounts, sagaCounts, err := m.Counts(ctx)
		if err != nil {
			m.log.WarnContext(ctx, "billing: gauge query failed", "error", err)
		}
		for status, n := range runCounts {
			ob.ObserveInt64(runs, n, metric.WithAttributes(attribute.String("status", status)))
		}
		for state, n := range sagaCounts {
			ob.ObserveInt64(sagas, n, metric.WithAttributes(attribute.String("state", state)))
		}
		return nil
	}, runs, sagas)
	return err
}
