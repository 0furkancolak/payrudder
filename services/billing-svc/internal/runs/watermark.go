package runs

import (
	"context"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// watermark proves that every on-time event of the run's period is in usage_aggregates (docs/07-correctness.md §4):
//
//  1. ingest's outbox holds no event ingested before the cutoff, so every on-time event is in Kafka;
//  2. the end offsets of usage.events.v1 are snapshotted (once, after step 1) into the run;
//  3. the aggregator has consumed up to those offsets.
//
// It reports false while any step is not yet true.
func (m *Manager) watermark(ctx context.Context, run *Run) (bool, error) {
	if run.Offsets == nil {
		w, err := m.cfg.Ingest.GetOutboxWatermark(ctx, &ingestv1.GetOutboxWatermarkRequest{})
		if err != nil {
			return false, fmt.Errorf("ingest watermark: %w", err)
		}
		if oldest := w.GetOldestUnpublishedIngestedAt(); oldest != nil && oldest.AsTime().Before(run.CutoffAt) {
			m.waiting.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "ingest_outbox")))
			return false, nil
		}
		offsets, err := m.cfg.EndOffsets(ctx)
		if err != nil {
			return false, fmt.Errorf("kafka end offsets: %w", err)
		}
		raw := make(map[string]int64, len(offsets))
		for p, o := range offsets {
			raw[fmt.Sprint(p)] = o
		}
		b, _ := json.Marshal(raw)
		if err := m.q.SetRunOffsets(ctx, sqlc.SetRunOffsetsParams{RequiredOffsets: b, TenantID: run.TenantID, PeriodID: run.PeriodID}); err != nil {
			return false, err
		}
		run.Offsets = offsets
	}
	resp, err := m.cfg.Aggregator.GetIngestWatermark(tenantCtx(ctx, run.TenantID), &aggregatorv1.GetIngestWatermarkRequest{RequiredOffsets: run.Offsets})
	if err != nil {
		return false, fmt.Errorf("aggregator watermark: %w", err)
	}
	if !resp.GetCaughtUp() {
		m.waiting.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "aggregator_behind")))
		return false, nil
	}
	return true, nil
}
