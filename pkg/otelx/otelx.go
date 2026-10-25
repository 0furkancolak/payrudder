// Package otelx initialises OpenTelemetry for a Payrudder process and provides the message-header carriers that
// keep one trace across HTTP, gRPC, Kafka and RabbitMQ hops.
package otelx

import (
	"context"
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Options configures Setup.
type Options struct {
	ServiceName string
	Version     string
	Environment string
	// OTLPEndpoint is host:port of an OTLP gRPC receiver. Empty keeps tracing in-process only (no export).
	OTLPEndpoint string
	// Registry receives the OTel metrics through the Prometheus exporter. It is served on /metrics by pkg/health.
	Registry *prometheus.Registry
	// SampleRatio is the head-sampling ratio for root spans. 0 means 1.0 (sample everything; the collector tail-samples).
	SampleRatio float64
}

// Providers holds the initialised providers. They are also installed as the global providers.
type Providers struct {
	Tracer *sdktrace.TracerProvider
	Meter  *sdkmetric.MeterProvider
}

// Shutdown flushes and stops both providers.
func (p *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(p.Tracer.Shutdown(ctx), p.Meter.Shutdown(ctx))
}

// Propagator is the W3C trace-context + baggage propagator used on every hop.
func Propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

// Setup creates the tracer and meter providers and installs them, plus Propagator, as OTel globals.
func Setup(ctx context.Context, opts Options) (*Providers, error) {
	if opts.ServiceName == "" {
		return nil, errors.New("otelx: ServiceName is required")
	}
	if opts.Registry == nil {
		return nil, errors.New("otelx: Registry is required")
	}
	res := resource.NewSchemaless(
		attribute.String("service.name", opts.ServiceName),
		attribute.String("service.version", opts.Version),
		attribute.String("deployment.environment.name", opts.Environment),
	)

	ratio := opts.SampleRatio
	if ratio <= 0 {
		ratio = 1
	}
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	}
	if opts.OTLPEndpoint != "" {
		exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(opts.OTLPEndpoint), otlptracegrpc.WithInsecure())
		if err != nil {
			return nil, fmt.Errorf("otelx: trace exporter: %w", err)
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)

	promExp, err := otelprom.New(otelprom.WithRegisterer(opts.Registry))
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("otelx: prometheus exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(promExp))

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(Propagator())
	return &Providers{Tracer: tp, Meter: mp}, nil
}
