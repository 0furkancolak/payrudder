// Package command is the write side of the events module: Ingest validates a batch of usage events, claims their
// idempotency keys and enqueues the accepted ones in the transactional outbox (docs/03-services/ingest-svc.md).
package command

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/lookup"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
)

// Topic is where accepted events go.
const Topic = "usage.events.v1"

// ceType is the fully-qualified protobuf message name, carried in the ce-type header.
const ceType = "payrudder.events.v1.UsageEvent"

const (
	// warmConcurrency bounds the lookups a batch starts at once to warm the meter and customer caches.
	warmConcurrency = 16
	// writeTimeout is the transaction_timeout of the write transaction.
	writeTimeout = "5s"
)

// Lookups resolves meters and customers (cached in production).
type Lookups interface {
	Meter(ctx context.Context, tenantID, code string) (lookup.Meter, bool, error)
	Customer(ctx context.Context, tenantID, externalID string) (lookup.Customer, bool, error)
}

// Options configure Commands. App and Lookup are required.
type Options struct {
	// App is the ingest_app pool (RLS enforced), or its router when cells are on: a tenant's transaction goes where the directory says its data is.
	App    pgxx.Beginner
	Lookup Lookups
	Log    *slog.Logger
	// MaxOutboxDepth makes Ingest answer RESOURCE_EXHAUSTED above this many pending rows. 0 disables it.
	MaxOutboxDepth int64
	// Depth is the current estimate of pending outbox rows (the outbox monitor). Nil counts as 0.
	Depth func() int64
	// Now is the clock used for validation (default time.Now). The persisted ingested_at comes from NowInTx.
	Now func() time.Time
	// NowInTx reads the database clock inside the write transaction (default: SELECT clock_timestamp()).
	NowInTx func(ctx context.Context, q *sqlc.Queries) (time.Time, error)
	// Propagator injects the trace context into outbox headers (default: the global propagator).
	Propagator propagation.TextMapPropagator
}

// Commands are the state-changing operations of the events module.
type Commands struct {
	o   Options
	log *slog.Logger

	locMu sync.Mutex
	locs  map[string]*time.Location

	events metric.Int64Counter
}

// New validates the options and registers the ingest_events counter.
func New(o Options) (*Commands, error) {
	if o.App == nil || o.Lookup == nil {
		return nil, errors.New("ingest: App and Lookup are required")
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Depth == nil {
		o.Depth = func() int64 { return 0 }
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NowInTx == nil {
		o.NowInTx = func(ctx context.Context, q *sqlc.Queries) (time.Time, error) { return q.ClockNow(ctx) }
	}
	if o.Propagator == nil {
		o.Propagator = otel.GetTextMapPropagator()
	}
	ctr, err := otel.Meter("github.com/0furkancolak/payrudder/services/ingest-svc").Int64Counter("ingest_events",
		metric.WithDescription("Ingested events by outcome"))
	if err != nil {
		return nil, err
	}
	return &Commands{o: o, log: o.Log, locs: map[string]*time.Location{}, events: ctr}, nil
}

func (c *Commands) location(tz string) (*time.Location, error) {
	c.locMu.Lock()
	defer c.locMu.Unlock()
	if l, ok := c.locs[tz]; ok {
		return l, nil
	}
	l, err := time.LoadLocation(tz)
	if err != nil {
		return nil, err
	}
	c.locs[tz] = l
	return l, nil
}
