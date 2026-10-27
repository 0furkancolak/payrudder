// Package monitor keeps a fresh estimate of the outbox depth and of the age of its oldest unpublished row, and exports both as
// gauges. The events module reads the depth to shed load when the relay falls behind.
package monitor

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/outbox/query"
)

// DefaultRefresh is how often the estimates are refreshed when Options.Refresh is not set.
const DefaultRefresh = time.Second

// Options configure a Monitor. System is required.
type Options struct {
	System  *pgxpool.Pool // ingest_system: depth and watermark
	Cells   *pgxx.Router  // the system-role router of cells mode; nil when cells are off
	Log     *slog.Logger
	Refresh time.Duration // default DefaultRefresh
}

// Monitor refreshes the depth estimate in Run.
type Monitor struct {
	system  *pgxpool.Pool
	cells   *pgxx.Router
	qry     *query.Queries
	log     *slog.Logger
	refresh time.Duration

	depth atomic.Int64
	// oldestMillis is the age of the oldest unpublished outbox row, refreshed with depth.
	oldestMillis atomic.Int64
}

// New registers the gauges. Call Run to keep the estimates fresh.
func New(o Options) (*Monitor, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Refresh <= 0 {
		o.Refresh = DefaultRefresh
	}
	m := &Monitor{system: o.System, cells: o.Cells, qry: query.New(o.System, o.Cells), log: o.Log, refresh: o.Refresh}
	meter := otel.Meter("github.com/0furkancolak/payrudder/services/ingest-svc")
	depthG, err := meter.Int64ObservableGauge("ingest_outbox_depth", metric.WithDescription("Estimated rows waiting in the outbox"))
	if err != nil {
		return nil, err
	}
	ageG, err := meter.Float64ObservableGauge("ingest_outbox_oldest_age_seconds", metric.WithUnit("s"),
		metric.WithDescription("Age of the oldest unpublished outbox row (0 when empty): relay lag and the watermark blocker"))
	if err != nil {
		return nil, err
	}
	if _, err := meter.RegisterCallback(func(_ context.Context, ob metric.Observer) error {
		ob.ObserveInt64(depthG, m.depth.Load())
		ob.ObserveFloat64(ageG, float64(m.oldestMillis.Load())/1000)
		return nil
	}, depthG, ageG); err != nil {
		return nil, err
	}
	return m, nil
}

// Depth is the last refreshed depth estimate.
func (m *Monitor) Depth() int64 { return m.depth.Load() }

// Run refreshes the estimates until ctx is done.
func (m *Monitor) Run(ctx context.Context) error {
	t := time.NewTicker(m.refresh)
	defer t.Stop()
	for {
		m.refreshOnce(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (m *Monitor) refreshOnce(ctx context.Context) {
	d, err := outbox.Depth(ctx, m.system)
	if err == nil && m.cells != nil {
		var cellDepth int64
		if cellDepth, err = outbox.CellsDepth(ctx, m.cells); err == nil {
			d += cellDepth
		}
	}
	switch {
	case err == nil:
		m.depth.Store(d)
	case ctx.Err() == nil:
		m.log.WarnContext(ctx, "ingest: outbox depth unavailable", "error", err)
	}
	oldest, ok, err := m.qry.WatermarkWithin(ctx, outbox.StatsPlacementAge)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			m.log.WarnContext(ctx, "ingest: outbox watermark unavailable", "error", err)
		}
	case ok:
		m.oldestMillis.Store(time.Since(oldest).Milliseconds())
	default:
		m.oldestMillis.Store(0)
	}
}
