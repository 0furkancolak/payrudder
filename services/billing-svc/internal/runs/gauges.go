package runs

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
		for _, q := range []struct {
			g     metric.Int64ObservableGauge
			sql   string
			label string
		}{
			{runs, `SELECT status, count(*) FROM billing_runs GROUP BY status`, "status"},
			{sagas, `SELECT state, count(*) FROM invoice_sagas GROUP BY state`, "state"},
		} {
			rows, err := m.cfg.Pool.Query(ctx, q.sql)
			if err != nil {
				m.log.WarnContext(ctx, "billing: gauge query failed", "error", err)
				continue
			}
			for rows.Next() {
				var k string
				var n int64
				if rows.Scan(&k, &n) == nil {
					ob.ObserveInt64(q.g, n, metric.WithAttributes(attribute.String(q.label, k)))
				}
			}
			rows.Close()
		}
		return nil
	}, runs, sagas)
	return err
}
