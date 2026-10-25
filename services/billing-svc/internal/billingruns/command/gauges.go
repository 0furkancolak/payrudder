package command

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

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
		runCounts, err := m.q.CountRunsByStatus(ctx)
		if err != nil {
			m.log.WarnContext(ctx, "billing: gauge query failed", "error", err)
		}
		for _, r := range runCounts {
			ob.ObserveInt64(runs, r.Runs, metric.WithAttributes(attribute.String("status", r.Status)))
		}
		sagaCounts, err := m.q.CountSagasByState(ctx)
		if err != nil {
			m.log.WarnContext(ctx, "billing: gauge query failed", "error", err)
		}
		for _, r := range sagaCounts {
			ob.ObserveInt64(sagas, r.Sagas, metric.WithAttributes(attribute.String("state", r.State)))
		}
		return nil
	}, runs, sagas)
	return err
}
