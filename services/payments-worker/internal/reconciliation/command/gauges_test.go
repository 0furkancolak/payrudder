package command

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// observed reads the gauge once, as a scrape would, as kind -> value.
func observed(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if g, ok := m.Data.(metricdata.Gauge[int64]); ok && m.Name == "reconciliation_discrepancies_open" {
				for _, dp := range g.DataPoints {
					kind, _ := dp.Attributes.Value("kind")
					out[kind.AsString()] = dp.Value
				}
			}
		}
	}
	return out
}

// The gauge reports what the store counts, by kind, on every scrape; a kind that has none has no series; a place that cannot be counted still reports the others.
func TestOpenGaugeReportsTheCountsOnEveryScrape(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
	counts, failure := map[string]int64{"settlement_missing": 2, "paid_without_charge": 1}, error(nil)
	if err := RegisterOpenGauge(meter, func(context.Context) (map[string]int64, error) { return counts, failure }, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	if got := observed(t, reader); len(got) != 2 || got["settlement_missing"] != 2 || got["paid_without_charge"] != 1 {
		t.Fatalf("first scrape = %v", got)
	}
	counts = map[string]int64{"settlement_missing": 1}
	if got := observed(t, reader); len(got) != 1 || got["settlement_missing"] != 1 {
		t.Fatalf("after one was resolved = %v (the kind with none has no series)", got)
	}
	failure = errors.New("a cell is down")
	if got := observed(t, reader); len(got) != 1 || got["settlement_missing"] != 1 {
		t.Fatalf("with a failure = %v (what could be counted is reported)", got)
	}
}
