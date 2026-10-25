package otelx_test

import (
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/0furkancolak/payrudder/pkg/otelx"
)

func TestInstrumentsKeepsFirstErrorAndHandsOutNoops(t *testing.T) {
	old := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider())
	t.Cleanup(func() { otel.SetMeterProvider(old) })

	b := otelx.NewInstruments("test/instruments")
	b.Counter("otelx_t_total", "ok")
	if b.Err() != nil {
		t.Fatalf("a valid counter failed: %v", b.Err())
	}
	bad := b.Counter("1 not valid", "bad") // the SDK refuses names that do not start with a letter
	h := b.Histogram("otelx_t_seconds", "after the failure", metric.WithUnit("s"))
	if b.Err() == nil {
		t.Fatal("the invalid name was accepted")
	}
	if bad == nil || h == nil {
		t.Fatal("instruments after a failure must be non-nil no-ops")
	}
}
