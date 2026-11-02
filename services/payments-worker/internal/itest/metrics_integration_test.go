//go:build integration

package itest

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// metricsReader reads the counters of the code under test. The global provider is set when the package loads, before any test runs: the instruments the handlers made
// at their own start-up are handed to it (OpenTelemetry's global delegation).
var metricsReader = func() *sdkmetric.ManualReader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

// counted is what the counter name has counted so far, over the series that carry every attribute in want (all of them when none is named). The alert rules of
// deploy/prometheus read these counters under the name plus _total.
func counted(t *testing.T, name string, want ...attribute.KeyValue) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := metricsReader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || m.Name != name {
				continue
			}
		points:
			for _, dp := range sum.DataPoints {
				for _, kv := range want {
					if v, ok := dp.Attributes.Value(kv.Key); !ok || v != kv.Value {
						continue points
					}
				}
				total += dp.Value
			}
		}
	}
	return total
}

// gauged is the value of the gauge name for the series with these attributes, and whether it has one (an observable gauge is read at collection).
func gauged(t *testing.T, name string, want ...attribute.KeyValue) (int64, bool) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := metricsReader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			g, ok := m.Data.(metricdata.Gauge[int64])
			if !ok || m.Name != name {
				continue
			}
		points:
			for _, dp := range g.DataPoints {
				for _, kv := range want {
					if v, ok := dp.Attributes.Value(kv.Key); !ok || v != kv.Value {
						continue points
					}
				}
				return dp.Value, true
			}
		}
	}
	return 0, false
}
