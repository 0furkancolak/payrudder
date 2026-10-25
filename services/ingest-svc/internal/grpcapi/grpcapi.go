// Package grpcapi wires the modules into IngestService: the events handlers, the outbox handlers and the outbox monitor
// the events module reads its back-pressure from.
package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/propagation"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/events"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/events/command"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/outbox"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/outbox/monitor"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/outbox/query"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
)

// Options configure a Server. App, System and Lookup are required.
type Options struct {
	App    *pgxpool.Pool // ingest_app: RLS enforced
	System *pgxpool.Pool // ingest_system: watermark and depth
	Lookup command.Lookups
	Log    *slog.Logger
	// MaxOutboxDepth makes IngestEvents answer RESOURCE_EXHAUSTED above this many pending rows. 0 disables it.
	MaxOutboxDepth int64
	// DepthRefresh is how often the depth estimate is refreshed (default 1s).
	DepthRefresh time.Duration
	// Now is the clock used for validation (default time.Now). The persisted ingested_at comes from NowInTx.
	Now func() time.Time
	// NowInTx reads the database clock inside the write transaction (default: SELECT clock_timestamp()).
	NowInTx func(ctx context.Context, q *sqlc.Queries) (time.Time, error)
	// Propagator injects the trace context into outbox headers (default: the global propagator).
	Propagator propagation.TextMapPropagator
}

// Server implements ingestv1.IngestServiceServer by delegating each method to the module that owns it.
type Server struct {
	ingestv1.UnimplementedIngestServiceServer
	events *events.Handler
	outbox *outbox.Handler
	mon    *monitor.Monitor
}

// New builds the modules. Call Run to keep the outbox depth estimate fresh.
func New(o Options) (*Server, error) {
	if o.System == nil {
		return nil, errors.New("ingest: System is required")
	}
	mon, err := monitor.New(monitor.Options{System: o.System, Log: o.Log, Refresh: o.DepthRefresh})
	if err != nil {
		return nil, err
	}
	cmd, err := command.New(command.Options{
		App: o.App, Lookup: o.Lookup, Log: o.Log, MaxOutboxDepth: o.MaxOutboxDepth, Depth: mon.Depth,
		Now: o.Now, NowInTx: o.NowInTx, Propagator: o.Propagator,
	})
	if err != nil {
		return nil, err
	}
	return &Server{events: events.New(cmd), outbox: outbox.New(query.New(o.System)), mon: mon}, nil
}

// IngestEvents implements ingestv1.IngestServiceServer.
func (s *Server) IngestEvents(ctx context.Context, req *ingestv1.IngestEventsRequest) (*ingestv1.IngestEventsResponse, error) {
	return s.events.IngestEvents(ctx, req)
}

// GetOutboxWatermark implements ingestv1.IngestServiceServer.
func (s *Server) GetOutboxWatermark(ctx context.Context, req *ingestv1.GetOutboxWatermarkRequest) (*ingestv1.GetOutboxWatermarkResponse, error) {
	return s.outbox.GetOutboxWatermark(ctx, req)
}

// Run refreshes the outbox depth estimate until ctx is done.
func (s *Server) Run(ctx context.Context) error { return s.mon.Run(ctx) }

// OutboxDepth is the last refreshed depth estimate.
func (s *Server) OutboxDepth() int64 { return s.mon.Depth() }
