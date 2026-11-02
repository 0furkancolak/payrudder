package command

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// RegisterOpenGauge exposes how many live discrepancies wait for an operator, by kind (reconciliation_discrepancies_open), read on every scrape: the alerts of plan 22 G6
// (ReconciliationBacklog, SettlementOverdue) watch it. A kind with none has no series. read is store.Discrepancies.OpenCounts; when it fails, what it could count is
// reported and the failure is logged (a place that could not be counted shows as fewer, never as more).
func RegisterOpenGauge(m metric.Meter, read func(context.Context) (map[string]int64, error), log *slog.Logger) error {
	open, err := m.Int64ObservableGauge("reconciliation_discrepancies_open", metric.WithDescription("Live discrepancies waiting for an operator (status open), by kind"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(ctx context.Context, ob metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		counts, err := read(ctx)
		if err != nil && log != nil {
			log.WarnContext(ctx, "reconciliation: the open discrepancies could not all be counted", "error", err)
		}
		for kind, n := range counts {
			ob.ObserveInt64(open, n, metric.WithAttributes(attribute.String("kind", kind)))
		}
		return nil
	}, open)
	return err
}
