package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/rating"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Re-rating prices a closed period again under another plan version, on the same usage, and bills the difference
// (docs/03-services/billing-svc.md#re-rating). Usage is never re-counted: only prices change.

// Errors a caller can tell apart.
var (
	ErrNoRun            = errors.New("runs: no billing run for that period")
	ErrRunNotCompleted  = errors.New("runs: the period's billing run is not COMPLETED")
	ErrUnknownPlanVer   = errors.New("runs: unknown plan version")
	ErrReratingNotFound = errors.New("runs: no such re-rating job")
)

// Re-rating job statuses.
const (
	ReratingRunning   = "RUNNING"
	ReratingCompleted = "COMPLETED"
	ReratingFailed    = "FAILED"
)

// ReratingJob is a rerating_jobs row.
type ReratingJob struct {
	ID, TenantID    uuid.UUID
	PeriodID        string
	PlanVersionID   uuid.UUID
	Generation      int32
	Status          string
	CustomersRated  int
	Corrections     int
	Credits         int
	CorrectionMinor int64
	CreditMinor     int64
	LastError       string
	CreatedAt       time.Time
	CompletedAt     *time.Time
}

func jobOf(r sqlc.ReratingJob) *ReratingJob {
	return &ReratingJob{ID: r.ID, TenantID: r.TenantID, PeriodID: r.PeriodID, PlanVersionID: r.PlanVersionID, Generation: r.Generation, Status: r.Status,
		CustomersRated: int(r.CustomersRated), Corrections: int(r.Corrections), Credits: int(r.Credits), CorrectionMinor: r.CorrectionMinor, CreditMinor: r.CreditMinor,
		LastError: deref(r.LastError), CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt}
}

// GetReratingJob loads a job.
func (m *Manager) GetReratingJob(ctx context.Context, tenantID, jobID uuid.UUID) (*ReratingJob, error) {
	row, err := m.q.GetReratingJob(ctx, sqlc.GetReratingJobParams{TenantID: tenantID, ID: jobID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReratingNotFound
	}
	if err != nil {
		return nil, err
	}
	return jobOf(row), nil
}

// StartRerating creates the job for (tenant, period, plan version) and runs it. A repeated request returns the
// existing job (created=false) and finishes it if a crash left it RUNNING: the result is the same either way, one
// correction or credit per customer at most.
func (m *Manager) StartRerating(ctx context.Context, tenantID uuid.UUID, periodID string, planVersionID uuid.UUID) (*ReratingJob, bool, error) {
	run, err := m.GetRun(ctx, tenantID, periodID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNoRun
	}
	if err != nil {
		return nil, false, err
	}
	if run.Status != Completed {
		return nil, false, ErrRunNotCompleted
	}
	resp, err := m.cfg.Plans.GetPlanVersion(tenantCtx(ctx, tenantID), &plansv1.GetPlanVersionRequest{PlanVersionId: planVersionID.String()})
	if err == nil && resp.GetPlanVersion() == nil {
		err = errors.New("not found")
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrUnknownPlanVer, err)
	}
	gen := int32(1)
	if run.Generation != nil {
		gen = *run.Generation
	}
	inserted, err := m.q.InsertReratingJob(ctx, sqlc.InsertReratingJobParams{TenantID: tenantID, ID: uuid.New(), PeriodID: periodID, PlanVersionID: planVersionID, Generation: gen})
	if err != nil {
		return nil, false, err
	}
	row, err := m.q.GetReratingJobByPlanVersion(ctx, sqlc.GetReratingJobByPlanVersionParams{TenantID: tenantID, PeriodID: periodID, PlanVersionID: planVersionID})
	if err != nil {
		return nil, false, err
	}
	job, created := jobOf(row), inserted == 1
	if job.Status == ReratingRunning {
		if job, err = m.rerate(ctx, job); err != nil {
			return job, created, err
		}
	}
	return job, created, nil
}

// ResumeRerating finishes jobs a crash left RUNNING (called by the scheduler loop).
func (m *Manager) ResumeRerating(ctx context.Context) error {
	rows, err := m.q.ListRunningReratingJobs(ctx, m.cfg.Now().Add(-time.Minute))
	if err != nil {
		return err
	}
	for _, r := range rows {
		j := jobOf(r)
		if _, err := m.rerate(ctx, j); err != nil {
			m.log.WarnContext(ctx, "re-rating: job failed to finish", "tenant", j.TenantID, "job", j.ID, "error", err)
		}
	}
	return nil
}

// billedComponents sums what a customer was already billed for the period's own usage: base fee, usage lines and the
// lines of earlier corrections, per meter ("" is the base fee). Late-usage and credit lines are other things.
func (m *Manager) billedComponents(ctx context.Context, tenantID uuid.UUID, periodID string) (map[string]map[string]int64, map[string]json.RawMessage, error) {
	rows, err := m.q.BilledComponents(ctx, sqlc.BilledComponentsParams{TenantID: tenantID, PeriodID: periodID})
	if err != nil {
		return nil, nil, err
	}
	billed := map[string]map[string]int64{}
	for _, r := range rows {
		if billed[r.CustomerID] == nil {
			billed[r.CustomerID] = map[string]int64{}
		}
		billed[r.CustomerID][r.MeterCode] = r.AmountMinor
	}
	// The snapshot of each customer's regular invoice (a correction repeats who it is addressed to).
	srows, err := m.q.ListRegularInvoiceSnapshots(ctx, sqlc.ListRegularInvoiceSnapshotsParams{TenantID: tenantID, PeriodID: periodID})
	if err != nil {
		return nil, nil, err
	}
	snaps := make(map[string]json.RawMessage, len(srows))
	for _, r := range srows {
		snaps[r.CustomerID] = r.Snapshot
	}
	return billed, snaps, nil
}

// rerate does the work of a job. Everything it writes is keyed by the job, so running it again completes what a
// crash interrupted and creates nothing twice.
func (m *Manager) rerate(ctx context.Context, job *ReratingJob) (*ReratingJob, error) {
	if err := m.rerateOnce(ctx, job); err != nil {
		var p permanent
		if errors.As(err, &p) {
			_ = m.q.FailReratingJob(ctx, sqlc.FailReratingJobParams{LastError: err.Error(), TenantID: job.TenantID, ID: job.ID})
		}
		return job, err
	}
	return m.GetReratingJob(ctx, job.TenantID, job.ID)
}

// reratePass is what one pass over a job reads before it prices any customer.
type reratePass struct {
	job      *ReratingJob
	tctx     context.Context
	currency string
	meters   map[string]meterInfo
	subs     map[string]subscription
	target   rating.PlanVersion
	planCode string
	usage    map[string][]rating.Usage
	billed   map[string]map[string]int64
	snaps    map[string]json.RawMessage
	codes    map[string]string // plan version id -> plan code
}

func (m *Manager) rerateOnce(ctx context.Context, job *ReratingJob) error {
	rp, err := m.loadRerating(ctx, job)
	if err != nil {
		return err
	}
	rated := 0
	for _, cid := range rp.customers() {
		priced, err := m.rerateCustomer(ctx, rp, cid)
		if err != nil {
			return err
		}
		if priced {
			rated++
		}
	}
	return m.finishRerating(ctx, job, rated)
}

// loadRerating reads the tenant, the period, the subscriptions, the target plan version, the period's usage and what was billed for it.
func (m *Manager) loadRerating(ctx context.Context, job *ReratingJob) (*reratePass, error) {
	tid := job.TenantID
	rp := &reratePass{job: job, tctx: tenantCtx(ctx, tid), usage: map[string][]rating.Usage{}, codes: map[string]string{}}
	tn, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	rp.currency = tn.GetTenant().GetCurrency()
	loc, err := time.LoadLocation(tn.GetTenant().GetTimezone())
	if err != nil {
		return nil, permanent{err}
	}
	start, end, err := period.Bounds(job.PeriodID, loc)
	if err != nil {
		return nil, permanent{err}
	}
	if rp.meters, err = m.meters(rp.tctx); err != nil {
		return nil, err
	}
	if rp.subs, err = m.subscriptions(ctx, tid, start, end); err != nil {
		return nil, err
	}
	if err := m.loadTarget(rp); err != nil {
		return nil, err
	}
	if err := m.streamReratingUsage(ctx, rp); err != nil {
		return nil, err
	}
	if rp.billed, rp.snaps, err = m.billedComponents(ctx, tid, job.PeriodID); err != nil {
		return nil, err
	}
	return rp, nil
}

// loadTarget reads the plan version the job prices under and the plan it belongs to.
func (m *Manager) loadTarget(rp *reratePass) error {
	resp, err := m.cfg.Plans.GetPlanVersion(rp.tctx, &plansv1.GetPlanVersionRequest{PlanVersionId: rp.job.PlanVersionID.String()})
	if err != nil {
		return fmt.Errorf("get plan version: %w", err)
	}
	if rp.target, err = convertPlanVersion(resp.GetPlanVersion(), "", ""); err != nil {
		return permanent{err}
	}
	rp.planCode = resp.GetPlanVersion().GetPlanCode()
	return nil
}

// streamReratingUsage reads the usage of the period at the tenant's active generation (a replay may have corrected it since the run).
func (m *Manager) streamReratingUsage(ctx context.Context, rp *reratePass) error {
	job := rp.job
	gen := int32(0)
	us, err := m.cfg.Aggregator.StreamPeriodUsage(rp.tctx, &aggregatorv1.StreamPeriodUsageRequest{PeriodId: job.PeriodID})
	if err != nil {
		return fmt.Errorf("stream usage: %w", err)
	}
	for {
		r, err := us.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream usage: %w", err)
		}
		u := r.GetUsage()
		gen = u.GetGeneration()
		q, err := decimal(u.GetQuantity())
		if err != nil {
			return permanent{err}
		}
		rp.usage[u.GetCustomerId()] = append(rp.usage[u.GetCustomerId()], rating.Usage{MeterCode: u.GetMeterCode(), Quantity: q, UnitLabel: rp.meters[u.GetMeterCode()].Unit})
	}
	if gen != 0 && gen != job.Generation {
		if err := m.q.SetReratingJobGeneration(ctx, sqlc.SetReratingJobGenerationParams{Generation: gen, TenantID: job.TenantID, ID: job.ID}); err != nil {
			return err
		}
		job.Generation = gen
	}
	return nil
}

// customers are the subscribed customers, in a stable order.
func (rp *reratePass) customers() []string {
	ids := make([]string, 0, len(rp.subs))
	for c := range rp.subs {
		ids = append(ids, c)
	}
	sort.Strings(ids)
	return ids
}

// planCodeOf is the plan code of a plan version.
func (m *Manager) planCodeOf(rp *reratePass, pvID string) (string, error) {
	if c, ok := rp.codes[pvID]; ok {
		return c, nil
	}
	resp, err := m.cfg.Plans.GetPlanVersion(rp.tctx, &plansv1.GetPlanVersionRequest{PlanVersionId: pvID})
	if err != nil {
		return "", fmt.Errorf("get plan version %s: %w", pvID, err)
	}
	rp.codes[pvID] = resp.GetPlanVersion().GetPlanCode()
	return rp.codes[pvID], nil
}

// rerateCustomer prices one customer under the target and records the difference; it reports whether the customer was priced.
func (m *Manager) rerateCustomer(ctx context.Context, rp *reratePass, cid string) (bool, error) {
	job, sub := rp.job, rp.subs[cid]
	snap, invoiced := rp.snaps[cid]
	if !invoiced || sub.PlanVersionID == job.PlanVersionID.String() {
		return false, nil // never billed for the period, or already on the target version
	}
	if (sub.Currency != "" && sub.Currency != rp.currency) || len(sub.Chain) > 1 {
		// Re-rating prices in the tenant currency and compares whole-period fees; a subscription sold in another
		// currency or changed inside the period would be compared against the wrong amounts.
		m.log.WarnContext(ctx, "re-rating skips a subscription in another currency or changed inside the period", "tenant", job.TenantID, "customer", cid, "period", job.PeriodID)
		return false, nil
	}
	code, err := m.planCodeOf(rp, sub.PlanVersionID)
	if err != nil {
		return false, err
	}
	if code != rp.planCode {
		return false, nil // another plan: not what this job re-prices
	}
	deltas, total, err := correctionDeltas(rp.target, rp.usage[cid], rp.billed[cid], rp.meters)
	if err != nil {
		return true, err
	}
	if total == 0 {
		return true, nil
	}
	cuid, err := uuid.Parse(cid)
	if err != nil {
		return true, permanent{err}
	}
	return true, m.recordRerating(ctx, job, cuid, snap, rp.currency, deltas, total, rp.meters)
}

// correctionDeltas prices usage under target and returns, per component (a meter, "" for the base fee), the lines that bring what was billed to the new price
// and their sum: positive when the customer owes more, negative when it is owed a credit.
func correctionDeltas(target rating.PlanVersion, usage []rating.Usage, billed map[string]int64, meters map[string]meterInfo) ([]rating.Line, int64, error) {
	lines, err := rating.Rate(target, usage)
	if err != nil {
		return nil, 0, permanent{err}
	}
	newAmount := map[string]int64{}
	newLine := map[string]rating.Line{}
	for _, l := range lines {
		newAmount[l.MeterCode] += l.AmountMinor
		newLine[l.MeterCode] = l
	}
	components := map[string]bool{}
	for k := range newAmount {
		components[k] = true
	}
	for k := range billed {
		components[k] = true
	}
	keys := make([]string, 0, len(components))
	for k := range components {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var deltas []rating.Line
	var total int64
	for _, k := range keys {
		d := newAmount[k] - billed[k]
		if d == 0 {
			continue
		}
		total += d
		l := rating.Line{Kind: rating.KindCorrection, MeterCode: k, AmountMinor: d, Description: "Base fee: price change"}
		if k != "" {
			l.Description = fmt.Sprintf("%s: price change", k)
			l.UnitLabel = meters[k].Unit
			if nl, ok := newLine[k]; ok {
				l.Quantity = nl.Quantity
			} else if q := quantityOf(usage, k); q != nil {
				l.Quantity = q
			}
		}
		deltas = append(deltas, l)
	}
	return deltas, total, nil
}

// finishRerating finalizes the correction invoices (number, open, saga; finalizing twice is a no-op) and completes the job.
func (m *Manager) finishRerating(ctx context.Context, job *ReratingJob, rated int) error {
	ids, err := m.q.ListDraftInvoiceIDsOfRerating(ctx, sqlc.ListDraftInvoiceIDsOfReratingParams{TenantID: job.TenantID, ReratingJobID: job.ID})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := m.Finalize(ctx, job.TenantID, id); err != nil {
			return err
		}
	}
	return m.q.CompleteReratingJob(ctx, sqlc.CompleteReratingJobParams{CompletedAt: m.cfg.Now(), CustomersRated: i32(rated), TenantID: job.TenantID, ID: job.ID})
}

func quantityOf(us []rating.Usage, meter string) *apd.Decimal {
	for _, u := range us {
		if u.MeterCode == meter {
			return u.Quantity
		}
	}
	return nil
}

// recordRerating writes one customer's outcome: a CORRECTION draft when the new price is higher, a credit balance when
// it is lower. Both are guarded by unique indexes on the job, so a rerun does nothing.
func (m *Manager) recordRerating(ctx context.Context, job *ReratingJob, customer uuid.UUID, snapshot json.RawMessage, currency string,
	deltas []rating.Line, total int64, meters map[string]meterInfo) error {
	return pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := m.q.WithTx(tx)
		if total < 0 {
			return q.InsertCreditBalanceForRerating(ctx, sqlc.InsertCreditBalanceForReratingParams{TenantID: job.TenantID, CustomerID: customer, ID: uuid.New(),
				AmountMinor: -total, SourceReratingJobID: job.ID})
		}
		id := uuid.New()
		_, err := q.InsertDraftInvoice(ctx, sqlc.InsertDraftInvoiceParams{
			TenantID: job.TenantID, ID: id, CustomerID: customer, PeriodID: job.PeriodID, Kind: "CORRECTION", PlanVersionID: job.PlanVersionID, Currency: currency,
			SubtotalMinor: total, TotalMinor: total, Snapshot: snapshot, ReratingJobID: &job.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // this job already corrected this customer
		}
		if err != nil {
			return err
		}
		lines, scales := make([]rating.Line, len(deltas)), make([]int, len(deltas))
		for i, l := range deltas {
			l.Description += " (" + job.PeriodID + ")"
			lines[i], scales[i] = l, meters[l.MeterCode].Scale
		}
		return insertLines(ctx, q, job.TenantID, id, lines, scales)
	})
}
