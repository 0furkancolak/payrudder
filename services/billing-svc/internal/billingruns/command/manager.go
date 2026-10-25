package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
	runquery "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/query"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Ingest is the part of ingest-svc the watermark needs.
type Ingest interface {
	GetOutboxWatermark(ctx context.Context, in *ingestv1.GetOutboxWatermarkRequest, opts ...grpc.CallOption) (*ingestv1.GetOutboxWatermarkResponse, error)
}

// Chore is a piece of upkeep another module owns (the resending of credit note refunds, ...) that the scheduler runs on every tick. A failure is logged as
// "scheduler: <Name>" and does not stop the tick.
type Chore struct {
	Name string
	Run  func(ctx context.Context) error
}

// lookback is how many closed periods the scheduler considers per tenant.
const lookback = 3

// Config configures a Manager.
type Config struct {
	Pool     *pgxpool.Pool // billing_system
	Engine   *sagacommand.Engine
	Inputs   *inputs.Inputs
	Invoices *invoicecmd.Commands
	Ingest   Ingest
	// EndOffsets snapshots the end offsets of usage.events.v1.
	EndOffsets func(ctx context.Context) (map[int32]int64, error)
	Now        func() time.Time
	// WatermarkDelay is how long after the cutoff the watermark check starts (default 30 s: longer than ingest's 5 s
	// transaction timeout).
	WatermarkDelay time.Duration
	// Chores run after the scheduler's own work on every tick, in order.
	Chores []Chore
	// PageSize is how many customers are rated per transaction (default 500).
	PageSize int
	Log      *slog.Logger
}

// Manager drives billing runs.
type Manager struct {
	cfg Config
	q   *sqlc.Queries
	in  *inputs.Inputs
	log *slog.Logger

	runsDone      metric.Int64Counter
	watermarkSecs metric.Float64Histogram
	waiting       metric.Int64Counter
	noSub         metric.Int64Counter
	runStageSecs  metric.Float64Histogram
}

// NewManager applies defaults and registers metrics.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.WatermarkDelay <= 0 {
		cfg.WatermarkDelay = 30 * time.Second
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 500
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	m := otel.Meter("github.com/0furkancolak/payrudder/services/billing-svc/runs")
	mg := &Manager{cfg: cfg, q: sqlc.New(cfg.Pool), in: cfg.Inputs, log: cfg.Log}
	var err error
	if mg.runsDone, err = m.Int64Counter("billing_runs_completed", metric.WithDescription("Billing runs that reached COMPLETED")); err != nil {
		return nil, err
	}
	if mg.watermarkSecs, err = m.Float64Histogram("billing_run_wait_watermark_seconds", metric.WithUnit("s"),
		metric.WithDescription("Time from a run's cutoff until its ingest watermark was proven (close latency)")); err != nil {
		return nil, err
	}
	if err := mg.registerGauges(m); err != nil {
		return nil, err
	}
	if mg.waiting, err = m.Int64Counter("billing_watermark_waits", metric.WithDescription("Watermark checks that had to wait, by reason")); err != nil {
		return nil, err
	}
	if mg.noSub, err = m.Int64Counter("billing_usage_without_subscription", metric.WithDescription("Customers with usage but no subscription in the period (not invoiced)")); err != nil {
		return nil, err
	}
	if mg.runStageSecs, err = m.Float64Histogram("billing_run_stage_seconds", metric.WithUnit("s"), metric.WithDescription("Time spent in a run stage")); err != nil {
		return nil, err
	}
	return mg, nil
}

// Now is the manager's clock (a test clock in tests).
func (m *Manager) Now() time.Time { return m.cfg.Now() }

// GetRun returns the run or pgx.ErrNoRows.
func (m *Manager) GetRun(ctx context.Context, tenantID uuid.UUID, periodID string) (*model.Run, error) {
	return runquery.Load(ctx, m.q, tenantID, periodID)
}

// EnsureRun creates the run if it is missing. With lowerCutoff an existing, unfinished run's cutoff is lowered to
// cutoff (the sandbox early close). It is idempotent per (tenant, period): calling it twice yields one run.
func (m *Manager) EnsureRun(ctx context.Context, tenantID uuid.UUID, periodID string, cutoff time.Time, lowerCutoff bool) (*model.Run, error) {
	if err := m.q.InsertBillingRun(ctx, sqlc.InsertBillingRunParams{TenantID: tenantID, PeriodID: periodID, CutoffAt: cutoff}); err != nil {
		return nil, err
	}
	if lowerCutoff {
		if err := m.q.LowerRunCutoff(ctx, sqlc.LowerRunCutoffParams{CutoffAt: cutoff, TenantID: tenantID, PeriodID: periodID}); err != nil {
			return nil, err
		}
	}
	return m.GetRun(ctx, tenantID, periodID)
}

func (m *Manager) setStatus(ctx context.Context, run *model.Run, status, lastErr string) error {
	return m.q.SetRunStatus(ctx, sqlc.SetRunStatusParams{Status: status, LastError: lastErr, TenantID: run.TenantID, PeriodID: run.PeriodID})
}

// withLock runs fn while holding the run's advisory lock on a dedicated connection; it returns false when another pod
// holds the lock. The lock is released when fn returns or the connection dies, so a crashed pod never blocks a run.
func (m *Manager) withLock(ctx context.Context, tenantID uuid.UUID, periodID string, fn func(ctx context.Context) error) (bool, error) {
	conn, err := m.cfg.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	q := sqlc.New(conn)
	key := tenantID.String() + "|" + periodID
	got, err := q.TryRunLock(ctx, key)
	if err != nil {
		return false, err
	}
	if !got {
		return false, nil
	}
	defer func() {
		// A cancelled ctx must not leave the lock behind on a pooled connection.
		if err := q.ReleaseRunLock(context.WithoutCancel(ctx), key); err != nil {
			m.log.WarnContext(ctx, "billing run lock not released", "tenant", tenantID, "period", periodID, "error", err)
		}
	}()
	return true, fn(ctx)
}

// Drive moves one run as far as it can go now. Waiting is not an error: the scheduler calls Drive again.
func (m *Manager) Drive(ctx context.Context, tenantID uuid.UUID, periodID string) (*model.Run, error) {
	ctx, span := otel.Tracer("github.com/0furkancolak/payrudder/services/billing-svc/runs").Start(ctx, "billing.run",
		trace.WithAttributes(attribute.String("payrudder.tenant_id", tenantID.String()), attribute.String("billing.period", periodID)))
	defer span.End()
	var out *model.Run
	_, err := m.withLock(ctx, tenantID, periodID, func(ctx context.Context) error {
		for {
			run, err := m.GetRun(ctx, tenantID, periodID)
			if err != nil {
				return err
			}
			out = run
			progressed, err := m.step(ctx, run)
			if err != nil {
				return err
			}
			if !progressed {
				out, err = m.GetRun(ctx, tenantID, periodID)
				return err
			}
		}
	})
	if err != nil {
		return out, err
	}
	if out == nil {
		return m.GetRun(ctx, tenantID, periodID)
	}
	return out, nil
}

// step performs the next stage of a run and reports whether the status changed (so Drive should look again).
func (m *Manager) step(ctx context.Context, run *model.Run) (bool, error) {
	now := m.cfg.Now()
	switch run.Status {
	case model.Completed, model.Failed:
		return false, nil
	case model.Scheduled:
		if now.Before(run.CutoffAt.Add(m.cfg.WatermarkDelay)) {
			return false, nil
		}
		return true, m.setStatus(ctx, run, model.WaitingWatermark, "")
	case model.WaitingWatermark:
		ok, err := m.watermark(ctx, run)
		if err != nil || !ok {
			return false, err
		}
		m.watermarkSecs.Record(ctx, max(0, now.Sub(run.CutoffAt).Seconds()))
		return true, m.setStatus(ctx, run, model.Rating, "")
	case model.Rating:
		start := time.Now()
		if err := m.rate(ctx, run); err != nil {
			m.log.ErrorContext(ctx, "rating failed", "tenant", run.TenantID, "period", run.PeriodID, "error", err)
			if inputs.IsTransient(err) {
				return false, err
			}
			return false, m.setStatus(ctx, run, model.Failed, err.Error())
		}
		m.runStageSecs.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attribute.String("stage", "rating")))
		return true, m.setStatus(ctx, run, model.Invoicing, "")
	case model.Invoicing:
		done, err := m.invoice(ctx, run)
		if err != nil || !done {
			return false, err
		}
		m.runsDone.Add(ctx, 1)
		return true, m.setStatus(ctx, run, model.Completed, "")
	}
	return false, fmt.Errorf("unknown run status %q", run.Status)
}

// RetryRun puts a FAILED run back to RATING (operator retry). Drafts already created are kept.
func (m *Manager) RetryRun(ctx context.Context, tenantID uuid.UUID, periodID string) error {
	n, err := m.q.RetryFailedRun(ctx, sqlc.RetryFailedRunParams{TenantID: tenantID, PeriodID: periodID})
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("run is not FAILED")
	}
	return nil
}
