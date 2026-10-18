// Package runs drives billing runs: the scheduler that finds due (tenant, period) pairs, the watermark protocol that
// proves every on-time event is aggregated, rating into draft invoices, finalization with gap-free numbers, and the
// completion check (docs/03-services/billing-svc.md#period-close-billing-run).
package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
)

// Status of a billing run.
const (
	Scheduled        = "SCHEDULED"
	WaitingWatermark = "WAITING_WATERMARK"
	Rating           = "RATING"
	Invoicing        = "INVOICING"
	Completed        = "COMPLETED"
	Failed           = "FAILED"
)

// Ingest is the part of ingest-svc the watermark needs.
type Ingest interface {
	GetOutboxWatermark(ctx context.Context, in *ingestv1.GetOutboxWatermarkRequest, opts ...grpc.CallOption) (*ingestv1.GetOutboxWatermarkResponse, error)
}

// Aggregator is the part of the aggregator billing uses.
type Aggregator interface {
	StreamPeriodUsage(ctx context.Context, in *aggregatorv1.StreamPeriodUsageRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[aggregatorv1.StreamPeriodUsageResponse], error)
	StreamLateUsage(ctx context.Context, in *aggregatorv1.StreamLateUsageRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[aggregatorv1.StreamLateUsageResponse], error)
	GetUsageBatch(ctx context.Context, in *aggregatorv1.GetUsageBatchRequest, opts ...grpc.CallOption) (*aggregatorv1.GetUsageBatchResponse, error)
	GetIngestWatermark(ctx context.Context, in *aggregatorv1.GetIngestWatermarkRequest, opts ...grpc.CallOption) (*aggregatorv1.GetIngestWatermarkResponse, error)
	FreezePeriod(ctx context.Context, in *aggregatorv1.FreezePeriodRequest, opts ...grpc.CallOption) (*aggregatorv1.FreezePeriodResponse, error)
	ClosePeriodEarly(ctx context.Context, in *aggregatorv1.ClosePeriodEarlyRequest, opts ...grpc.CallOption) (*aggregatorv1.ClosePeriodEarlyResponse, error)
}

// Plans is the part of plans-svc billing uses (PlansService and AdminService).
type Plans interface {
	GetCustomer(ctx context.Context, in *plansv1.GetCustomerRequest, opts ...grpc.CallOption) (*plansv1.GetCustomerResponse, error)
	ListMeters(ctx context.Context, in *plansv1.ListMetersRequest, opts ...grpc.CallOption) (*plansv1.ListMetersResponse, error)
	GetPlanVersion(ctx context.Context, in *plansv1.GetPlanVersionRequest, opts ...grpc.CallOption) (*plansv1.GetPlanVersionResponse, error)
	ListSubscriptionsForPeriod(ctx context.Context, in *plansv1.ListSubscriptionsForPeriodRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[plansv1.ListSubscriptionsForPeriodResponse], error)
}

// Admin is the part of plans-svc's AdminService billing uses.
type Admin interface {
	GetTenant(ctx context.Context, in *plansv1.GetTenantRequest, opts ...grpc.CallOption) (*plansv1.GetTenantResponse, error)
	ListTenants(ctx context.Context, in *plansv1.ListTenantsRequest, opts ...grpc.CallOption) (*plansv1.ListTenantsResponse, error)
}

// Config configures a Manager.
type Config struct {
	Pool       *pgxpool.Pool // billing_system
	Engine     *saga.Engine
	Plans      Plans
	Admin      Admin
	Ingest     Ingest
	Aggregator Aggregator
	// EndOffsets snapshots the end offsets of usage.events.v1.
	EndOffsets func(ctx context.Context) (map[int32]int64, error)
	Now        func() time.Time
	// WatermarkDelay is how long after the cutoff the watermark check starts (default 30 s: longer than ingest's 5 s
	// transaction timeout).
	WatermarkDelay time.Duration
	// PageSize is how many customers are rated per transaction (default 500).
	PageSize int
	// Lookback is how many closed periods the scheduler considers per tenant (default 3).
	Lookback int
	Log      *slog.Logger
}

// Manager drives billing runs.
type Manager struct {
	cfg Config
	log *slog.Logger

	runsDone     metric.Int64Counter
	waiting      metric.Int64Counter
	invoices     metric.Int64Counter
	noSub        metric.Int64Counter
	runStageSecs metric.Float64Histogram
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
	if cfg.Lookback <= 0 {
		cfg.Lookback = 3
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	m := otel.Meter("github.com/0furkancolak/quotify/services/billing-svc/runs")
	mg := &Manager{cfg: cfg, log: cfg.Log}
	var err error
	if mg.runsDone, err = m.Int64Counter("billing_runs_completed", metric.WithDescription("Billing runs that reached COMPLETED")); err != nil {
		return nil, err
	}
	if mg.waiting, err = m.Int64Counter("billing_watermark_waits", metric.WithDescription("Watermark checks that had to wait, by reason")); err != nil {
		return nil, err
	}
	if mg.invoices, err = m.Int64Counter("billing_invoices_drafted", metric.WithDescription("Draft invoices created")); err != nil {
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

// Run is a billing_runs row.
type Run struct {
	TenantID     uuid.UUID
	PeriodID     string
	Status       string
	CutoffAt     time.Time
	Generation   *int32
	InvoiceCount int
	LastError    string
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	Offsets      map[int32]int64
}

func scanRun(row pgx.Row) (*Run, error) {
	var r Run
	var raw []byte
	var lastErr *string
	if err := row.Scan(&r.TenantID, &r.PeriodID, &r.Status, &r.CutoffAt, &r.Generation, &raw, &r.InvoiceCount, &lastErr,
		&r.CreatedAt, &r.StartedAt, &r.CompletedAt); err != nil {
		return nil, err
	}
	if lastErr != nil {
		r.LastError = *lastErr
	}
	if len(raw) > 0 && string(raw) != "null" {
		m := map[string]int64{}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		r.Offsets = map[int32]int64{}
		for k, v := range m {
			var p int32
			if _, err := fmt.Sscan(k, &p); err == nil {
				r.Offsets[p] = v
			}
		}
	}
	return &r, nil
}

const runColumns = `tenant_id, period_id, status, cutoff_at, generation, required_offsets, invoice_count, last_error, created_at, started_at, completed_at`

// GetRun returns the run or pgx.ErrNoRows.
func (m *Manager) GetRun(ctx context.Context, tenantID uuid.UUID, periodID string) (*Run, error) {
	return scanRun(m.cfg.Pool.QueryRow(ctx, `SELECT `+runColumns+` FROM billing_runs WHERE tenant_id = $1 AND period_id = $2`, tenantID, periodID))
}

// EnsureRun creates the run if it is missing. With lowerCutoff an existing, unfinished run's cutoff is lowered to
// cutoff (the sandbox early close). It is idempotent per (tenant, period): calling it twice yields one run.
func (m *Manager) EnsureRun(ctx context.Context, tenantID uuid.UUID, periodID string, cutoff time.Time, lowerCutoff bool) (*Run, error) {
	if _, err := m.cfg.Pool.Exec(ctx, `INSERT INTO billing_runs (tenant_id, period_id, status, cutoff_at) VALUES ($1,$2,'SCHEDULED',$3)
		ON CONFLICT (tenant_id, period_id) DO NOTHING`, tenantID, periodID, cutoff); err != nil {
		return nil, err
	}
	if lowerCutoff {
		if _, err := m.cfg.Pool.Exec(ctx, `UPDATE billing_runs SET cutoff_at = LEAST(cutoff_at, $3)
			WHERE tenant_id = $1 AND period_id = $2 AND status IN ('SCHEDULED','WAITING_WATERMARK')`, tenantID, periodID, cutoff); err != nil {
			return nil, err
		}
	}
	return m.GetRun(ctx, tenantID, periodID)
}

func (m *Manager) setStatus(ctx context.Context, tx pgxQuerier, tenantID uuid.UUID, periodID, status, lastErr string) error {
	_, err := tx.Exec(ctx, `UPDATE billing_runs SET status = $3, last_error = NULLIF($4, ''),
		started_at = COALESCE(started_at, CASE WHEN $3 <> 'SCHEDULED' THEN now() END),
		completed_at = CASE WHEN $3 = 'COMPLETED' THEN now() ELSE completed_at END
		WHERE tenant_id = $1 AND period_id = $2`, tenantID, periodID, status, lastErr)
	return err
}

type pgxQuerier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// permanent marks an error that retrying cannot fix: the run goes to FAILED for the operator.
type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

// withLock runs fn while holding the run's advisory lock on a dedicated connection; it returns false when another pod
// holds the lock. The lock is released when fn returns or the connection dies, so a crashed pod never blocks a run.
func (m *Manager) withLock(ctx context.Context, tenantID uuid.UUID, periodID string, fn func(ctx context.Context) error) (bool, error) {
	conn, err := m.cfg.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var got bool
	key := tenantID.String() + "|" + periodID
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&got); err != nil {
		return false, err
	}
	if !got {
		return false, nil
	}
	defer func() {
		// A cancelled ctx must not leave the lock behind on a pooled connection.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key)
	}()
	return true, fn(ctx)
}

// Drive moves one run as far as it can go now. Waiting is not an error: the scheduler calls Drive again.
func (m *Manager) Drive(ctx context.Context, tenantID uuid.UUID, periodID string) (*Run, error) {
	var out *Run
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
func (m *Manager) step(ctx context.Context, run *Run) (bool, error) {
	now := m.cfg.Now()
	switch run.Status {
	case Completed, Failed:
		return false, nil
	case Scheduled:
		if now.Before(run.CutoffAt.Add(m.cfg.WatermarkDelay)) {
			return false, nil
		}
		return true, m.setStatus(ctx, m.cfg.Pool, run.TenantID, run.PeriodID, WaitingWatermark, "")
	case WaitingWatermark:
		ok, err := m.watermark(ctx, run)
		if err != nil || !ok {
			return false, err
		}
		return true, m.setStatus(ctx, m.cfg.Pool, run.TenantID, run.PeriodID, Rating, "")
	case Rating:
		start := time.Now()
		if err := m.rate(ctx, run); err != nil {
			m.log.ErrorContext(ctx, "rating failed", "tenant", run.TenantID, "period", run.PeriodID, "error", err)
			if isTransient(err) {
				return false, err
			}
			return false, m.setStatus(ctx, m.cfg.Pool, run.TenantID, run.PeriodID, Failed, err.Error())
		}
		m.runStageSecs.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attribute.String("stage", "rating")))
		return true, m.setStatus(ctx, m.cfg.Pool, run.TenantID, run.PeriodID, Invoicing, "")
	case Invoicing:
		done, err := m.invoice(ctx, run)
		if err != nil || !done {
			return false, err
		}
		m.runsDone.Add(ctx, 1)
		return true, m.setStatus(ctx, m.cfg.Pool, run.TenantID, run.PeriodID, Completed, "")
	}
	return false, fmt.Errorf("unknown run status %q", run.Status)
}

// RetryRun puts a FAILED run back to RATING (operator retry). Drafts already created are kept.
func (m *Manager) RetryRun(ctx context.Context, tenantID uuid.UUID, periodID string) error {
	tag, err := m.cfg.Pool.Exec(ctx, `UPDATE billing_runs SET status = 'RATING', last_error = NULL WHERE tenant_id = $1 AND period_id = $2 AND status = 'FAILED'`, tenantID, periodID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("run is not FAILED")
	}
	return nil
}

// tenantCtx returns a context carrying the tenant for calls to tenant-scoped services.
func tenantCtx(ctx context.Context, id uuid.UUID) context.Context {
	return tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: id}))
}

// operatorCtx returns a context for operator-only calls.
func operatorCtx(ctx context.Context) context.Context {
	return tenant.OutgoingContext(tenant.WithOperator(ctx))
}

func isTransient(err error) bool {
	var p permanent
	return !errors.As(err, &p)
}
