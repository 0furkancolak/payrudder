package support

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// RegisterGauges exposes the help desk's queue, read on every scrape: support_tickets_waiting (status open: waiting for an operator) and
// support_tickets_sla_breached (of those, the ones whose first answer is overdue). The alert SupportAnswerOverdue (deploy/prometheus/rules) watches the second.
// When the count fails nothing is reported for that scrape and the failure is logged: a missing reading never reads as "no ticket is late".
func (s *Service) RegisterGauges(m metric.Meter) error {
	waiting, err := m.Int64ObservableGauge("support_tickets_waiting", metric.WithDescription("Help desk tickets waiting for an operator (status open)"))
	if err != nil {
		return err
	}
	late, err := m.Int64ObservableGauge("support_tickets_sla_breached", metric.WithDescription("Help desk tickets whose first answer is overdue and still not given"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(ctx context.Context, ob metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		c, err := sqlc.New(s.pool).SupportOperatorCounts(ctx)
		if err != nil {
			s.log.WarnContext(ctx, "support: the queue could not be counted", slog.Any("error", err))
			return nil
		}
		ob.ObserveInt64(waiting, int64(c.Waiting))
		ob.ObserveInt64(late, int64(c.SlaBreached))
		return nil
	}, waiting, late)
	return err
}
