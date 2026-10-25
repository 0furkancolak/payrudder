package otelx

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// Instruments registers the metrics of one instrumentation scope and remembers the first failure, so a service creates
// all its counters and checks Err once instead of after each:
//
//	b := otelx.NewInstruments("github.com/.../services/x")
//	a.applied = b.Counter("x_applied", "Events applied")
//	if err := b.Err(); err != nil { return err }
//
// After a failure the methods return no-op instruments, so the half-built struct is safe to hold until Err is read.
type Instruments struct {
	meter metric.Meter
	err   error
}

// NewInstruments starts a builder on the global meter provider for the given scope name.
func NewInstruments(scope string) *Instruments {
	return &Instruments{meter: otel.Meter(scope)}
}

// Meter is the underlying meter, for instruments the builder has no method for (observable gauges).
func (b *Instruments) Meter() metric.Meter { return b.meter }

// Err is the first registration error, or nil.
func (b *Instruments) Err() error { return b.err }

// Counter registers an Int64Counter with a description.
func (b *Instruments) Counter(name, desc string, opts ...metric.Int64CounterOption) metric.Int64Counter {
	if b.err != nil {
		return noop.Int64Counter{}
	}
	c, err := b.meter.Int64Counter(name, append([]metric.Int64CounterOption{metric.WithDescription(desc)}, opts...)...)
	if err != nil {
		b.err = err
		return noop.Int64Counter{}
	}
	return c
}

// Histogram registers a Float64Histogram with a description.
func (b *Instruments) Histogram(name, desc string, opts ...metric.Float64HistogramOption) metric.Float64Histogram {
	if b.err != nil {
		return noop.Float64Histogram{}
	}
	h, err := b.meter.Float64Histogram(name, append([]metric.Float64HistogramOption{metric.WithDescription(desc)}, opts...)...)
	if err != nil {
		b.err = err
		return noop.Float64Histogram{}
	}
	return h
}
