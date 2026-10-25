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
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/saga"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
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
	GetTaxRates(ctx context.Context, in *plansv1.GetTaxRatesRequest, opts ...grpc.CallOption) (*plansv1.GetTaxRatesResponse, error)
	ListSubscriptionsFeeDue(ctx context.Context, in *plansv1.ListSubscriptionsFeeDueRequest, opts ...grpc.CallOption) (*plansv1.ListSubscriptionsFeeDueResponse, error)
	AdvanceSubscriptionFee(ctx context.Context, in *plansv1.AdvanceSubscriptionFeeRequest, opts ...grpc.CallOption) (*plansv1.AdvanceSubscriptionFeeResponse, error)
	ApplyScheduledChanges(ctx context.Context, in *plansv1.ApplyScheduledChangesRequest, opts ...grpc.CallOption) (*plansv1.ApplyScheduledChangesResponse, error)
	ListSubscriptionAdjustments(ctx context.Context, in *plansv1.ListSubscriptionAdjustmentsRequest, opts ...grpc.CallOption) (*plansv1.ListSubscriptionAdjustmentsResponse, error)
	MarkSubscriptionAdjustmentBilled(ctx context.Context, in *plansv1.MarkSubscriptionAdjustmentBilledRequest, opts ...grpc.CallOption) (*plansv1.MarkSubscriptionAdjustmentBilledResponse, error)
	RedeemPromotion(ctx context.Context, in *plansv1.RedeemPromotionRequest, opts ...grpc.CallOption) (*plansv1.RedeemPromotionResponse, error)
	ReversePromotion(ctx context.Context, in *plansv1.ReversePromotionRequest, opts ...grpc.CallOption) (*plansv1.ReversePromotionResponse, error)
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
	Events     Events // optional: without it tenant events stay in tenant_event_outbox
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
	q   *sqlc.Queries
	log *slog.Logger

	runsDone      metric.Int64Counter
	watermarkSecs metric.Float64Histogram
	waiting       metric.Int64Counter
	invoices      metric.Int64Counter
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
	if cfg.Lookback <= 0 {
		cfg.Lookback = 3
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	m := otel.Meter("github.com/0furkancolak/payrudder/services/billing-svc/runs")
	mg := &Manager{cfg: cfg, q: sqlc.New(cfg.Pool), log: cfg.Log}
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

func runOf(row sqlc.BillingRun) (*Run, error) {
	r := &Run{TenantID: row.TenantID, PeriodID: row.PeriodID, Status: row.Status, CutoffAt: row.CutoffAt, Generation: row.Generation, InvoiceCount: int(row.InvoiceCount),
		CreatedAt: row.CreatedAt, StartedAt: row.StartedAt, CompletedAt: row.CompletedAt}
	if row.LastError != nil {
		r.LastError = *row.LastError
	}
	if len(row.RequiredOffsets) > 0 && string(row.RequiredOffsets) != "null" {
		m := map[string]int64{}
		if err := json.Unmarshal(row.RequiredOffsets, &m); err != nil {
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
	return r, nil
}

// GetRun returns the run or pgx.ErrNoRows.
func (m *Manager) GetRun(ctx context.Context, tenantID uuid.UUID, periodID string) (*Run, error) {
	row, err := m.q.GetBillingRun(ctx, sqlc.GetBillingRunParams{TenantID: tenantID, PeriodID: periodID})
	if err != nil {
		return nil, err
	}
	return runOf(row)
}

// EnsureRun creates the run if it is missing. With lowerCutoff an existing, unfinished run's cutoff is lowered to
// cutoff (the sandbox early close). It is idempotent per (tenant, period): calling it twice yields one run.
func (m *Manager) EnsureRun(ctx context.Context, tenantID uuid.UUID, periodID string, cutoff time.Time, lowerCutoff bool) (*Run, error) {
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

func (m *Manager) setStatus(ctx context.Context, run *Run, status, lastErr string) error {
	return m.q.SetRunStatus(ctx, sqlc.SetRunStatusParams{Status: status, LastError: lastErr, TenantID: run.TenantID, PeriodID: run.PeriodID})
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
		_ = q.ReleaseRunLock(context.WithoutCancel(ctx), key)
	}()
	return true, fn(ctx)
}

// Drive moves one run as far as it can go now. Waiting is not an error: the scheduler calls Drive again.
func (m *Manager) Drive(ctx context.Context, tenantID uuid.UUID, periodID string) (*Run, error) {
	ctx, span := otel.Tracer("github.com/0furkancolak/payrudder/services/billing-svc/runs").Start(ctx, "billing.run",
		trace.WithAttributes(attribute.String("payrudder.tenant_id", tenantID.String()), attribute.String("billing.period", periodID)))
	defer span.End()
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
		return true, m.setStatus(ctx, run, WaitingWatermark, "")
	case WaitingWatermark:
		ok, err := m.watermark(ctx, run)
		if err != nil || !ok {
			return false, err
		}
		m.watermarkSecs.Record(ctx, max(0, now.Sub(run.CutoffAt).Seconds()))
		return true, m.setStatus(ctx, run, Rating, "")
	case Rating:
		start := time.Now()
		if err := m.rate(ctx, run); err != nil {
			m.log.ErrorContext(ctx, "rating failed", "tenant", run.TenantID, "period", run.PeriodID, "error", err)
			if isTransient(err) {
				return false, err
			}
			return false, m.setStatus(ctx, run, Failed, err.Error())
		}
		m.runStageSecs.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attribute.String("stage", "rating")))
		return true, m.setStatus(ctx, run, Invoicing, "")
	case Invoicing:
		done, err := m.invoice(ctx, run)
		if err != nil || !done {
			return false, err
		}
		m.runsDone.Add(ctx, 1)
		return true, m.setStatus(ctx, run, Completed, "")
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
