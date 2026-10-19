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

const reratingColumns = `id, tenant_id, period_id, plan_version_id, generation, status, customers_rated, corrections, credits,
	correction_minor, credit_minor, COALESCE(last_error, ''), created_at, completed_at`

func scanJob(row pgx.Row) (*ReratingJob, error) {
	var j ReratingJob
	err := row.Scan(&j.ID, &j.TenantID, &j.PeriodID, &j.PlanVersionID, &j.Generation, &j.Status, &j.CustomersRated, &j.Corrections,
		&j.Credits, &j.CorrectionMinor, &j.CreditMinor, &j.LastError, &j.CreatedAt, &j.CompletedAt)
	return &j, err
}

// GetReratingJob loads a job.
func (m *Manager) GetReratingJob(ctx context.Context, tenantID, jobID uuid.UUID) (*ReratingJob, error) {
	j, err := scanJob(m.cfg.Pool.QueryRow(ctx, `SELECT `+reratingColumns+` FROM rerating_jobs WHERE tenant_id = $1 AND id = $2`, tenantID, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReratingNotFound
	}
	return j, err
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
	tag, err := m.cfg.Pool.Exec(ctx, `INSERT INTO rerating_jobs (tenant_id, id, period_id, plan_version_id, generation, status)
		VALUES ($1,$2,$3,$4,$5,'RUNNING') ON CONFLICT (tenant_id, period_id, plan_version_id) DO NOTHING`,
		tenantID, uuid.New(), periodID, planVersionID, gen)
	if err != nil {
		return nil, false, err
	}
	job, err := scanJob(m.cfg.Pool.QueryRow(ctx, `SELECT `+reratingColumns+` FROM rerating_jobs WHERE tenant_id = $1 AND period_id = $2 AND plan_version_id = $3`,
		tenantID, periodID, planVersionID))
	if err != nil {
		return nil, false, err
	}
	created := tag.RowsAffected() == 1
	if job.Status == ReratingRunning {
		if job, err = m.rerate(ctx, job); err != nil {
			return job, created, err
		}
	}
	return job, created, nil
}

// ResumeRerating finishes jobs a crash left RUNNING (called by the scheduler loop).
func (m *Manager) ResumeRerating(ctx context.Context) error {
	rows, err := m.cfg.Pool.Query(ctx, `SELECT `+reratingColumns+` FROM rerating_jobs WHERE status = 'RUNNING' AND created_at < $1 ORDER BY created_at LIMIT 20`,
		m.cfg.Now().Add(-time.Minute))
	if err != nil {
		return err
	}
	var jobs []*ReratingJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	for _, j := range jobs {
		if _, err := m.rerate(ctx, j); err != nil {
			m.log.WarnContext(ctx, "re-rating: job failed to finish", "tenant", j.TenantID, "job", j.ID, "error", err)
		}
	}
	return rows.Err()
}

// billedComponents sums what a customer was already billed for the period's own usage: base fee, usage lines and the
// lines of earlier corrections, per meter ("" is the base fee). Late-usage and credit lines are other things.
func (m *Manager) billedComponents(ctx context.Context, tenantID uuid.UUID, periodID string) (map[string]map[string]int64, map[string]json.RawMessage, error) {
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT i.customer_id::text, COALESCE(l.meter_code, ''), sum(l.amount_minor)::bigint
		FROM invoices i JOIN invoice_lines l ON l.tenant_id = i.tenant_id AND l.invoice_id = i.id
		WHERE i.tenant_id = $1 AND i.period_id = $2 AND i.kind IN ('REGULAR','CORRECTION') AND i.status <> 'void'
		  AND l.kind IN ('BASE_FEE','USAGE','CORRECTION')
		GROUP BY 1, 2`, tenantID, periodID)
	if err != nil {
		return nil, nil, err
	}
	billed := map[string]map[string]int64{}
	for rows.Next() {
		var c, meter string
		var amount int64
		if err := rows.Scan(&c, &meter, &amount); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if billed[c] == nil {
			billed[c] = map[string]int64{}
		}
		billed[c][meter] = amount
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// The snapshot of each customer's regular invoice (a correction repeats who it is addressed to).
	srows, err := m.cfg.Pool.Query(ctx, `SELECT customer_id::text, snapshot FROM invoices WHERE tenant_id = $1 AND period_id = $2 AND kind = 'REGULAR' AND status <> 'void'`, tenantID, periodID)
	if err != nil {
		return nil, nil, err
	}
	defer srows.Close()
	snaps := map[string]json.RawMessage{}
	for srows.Next() {
		var c string
		var s json.RawMessage
		if err := srows.Scan(&c, &s); err != nil {
			return nil, nil, err
		}
		snaps[c] = s
	}
	return billed, snaps, srows.Err()
}

// rerate does the work of a job. Everything it writes is keyed by the job, so running it again completes what a
// crash interrupted and creates nothing twice.
func (m *Manager) rerate(ctx context.Context, job *ReratingJob) (*ReratingJob, error) {
	if err := m.rerateOnce(ctx, job); err != nil {
		var p permanent
		if errors.As(err, &p) {
			_, _ = m.cfg.Pool.Exec(ctx, `UPDATE rerating_jobs SET status = 'FAILED', last_error = $3 WHERE tenant_id = $1 AND id = $2`, job.TenantID, job.ID, err.Error())
		}
		return job, err
	}
	return m.GetReratingJob(ctx, job.TenantID, job.ID)
}

func (m *Manager) rerateOnce(ctx context.Context, job *ReratingJob) error {
	tid, tctx := job.TenantID, tenantCtx(ctx, job.TenantID)
	tn, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		return fmt.Errorf("get tenant: %w", err)
	}
	loc, err := time.LoadLocation(tn.GetTenant().GetTimezone())
	if err != nil {
		return permanent{err}
	}
	start, end, err := period.Bounds(job.PeriodID, loc)
	if err != nil {
		return permanent{err}
	}
	metersResp, err := m.cfg.Plans.ListMeters(tctx, &plansv1.ListMetersRequest{})
	if err != nil {
		return fmt.Errorf("list meters: %w", err)
	}
	meters := map[string]meterInfo{}
	for _, mt := range metersResp.GetMeters() {
		meters[mt.GetCode()] = meterInfo{Unit: mt.GetUnitLabel(), Scale: int(mt.GetQuantityScale())}
	}
	subs, err := m.subscriptions(ctx, tid, start, end)
	if err != nil {
		return err
	}

	// The target plan version and the plan it belongs to.
	tgtResp, err := m.cfg.Plans.GetPlanVersion(tctx, &plansv1.GetPlanVersionRequest{PlanVersionId: job.PlanVersionID.String()})
	if err != nil {
		return fmt.Errorf("get plan version: %w", err)
	}
	target, err := convertPlanVersion(tgtResp.GetPlanVersion())
	if err != nil {
		return permanent{err}
	}
	planCode := tgtResp.GetPlanVersion().GetPlanCode()

	// Usage of the period at the tenant's active generation (a replay may have corrected it since the run).
	usage := map[string][]rating.Usage{}
	gen := int32(0)
	us, err := m.cfg.Aggregator.StreamPeriodUsage(tctx, &aggregatorv1.StreamPeriodUsageRequest{PeriodId: job.PeriodID})
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
		usage[u.GetCustomerId()] = append(usage[u.GetCustomerId()], rating.Usage{MeterCode: u.GetMeterCode(), Quantity: q, UnitLabel: meters[u.GetMeterCode()].Unit})
	}
	if gen != 0 && gen != job.Generation {
		if _, err := m.cfg.Pool.Exec(ctx, `UPDATE rerating_jobs SET generation = $3 WHERE tenant_id = $1 AND id = $2`, tid, job.ID, gen); err != nil {
			return err
		}
		job.Generation = gen
	}

	billed, snaps, err := m.billedComponents(ctx, tid, job.PeriodID)
	if err != nil {
		return err
	}

	versions := map[string]string{} // plan version id -> plan code
	planOf := func(pvID string) (string, error) {
		if c, ok := versions[pvID]; ok {
			return c, nil
		}
		resp, err := m.cfg.Plans.GetPlanVersion(tctx, &plansv1.GetPlanVersionRequest{PlanVersionId: pvID})
		if err != nil {
			return "", fmt.Errorf("get plan version %s: %w", pvID, err)
		}
		versions[pvID] = resp.GetPlanVersion().GetPlanCode()
		return versions[pvID], nil
	}

	customers := make([]string, 0, len(subs))
	for c := range subs {
		customers = append(customers, c)
	}
	sort.Strings(customers)

	rated := 0
	for _, cid := range customers {
		sub := subs[cid]
		snap, invoiced := snaps[cid]
		if !invoiced || sub.PlanVersionID == job.PlanVersionID.String() {
			continue // never billed for the period, or already on the target version
		}
		code, err := planOf(sub.PlanVersionID)
		if err != nil {
			return err
		}
		if code != planCode {
			continue // another plan: not what this job re-prices
		}
		rated++
		lines, err := rating.Rate(target, usage[cid])
		if err != nil {
			return permanent{err}
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
		for k := range billed[cid] {
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
			d := newAmount[k] - billed[cid][k]
			if d == 0 {
				continue
			}
			total += d
			desc := "Base fee: price change"
			l := rating.Line{Kind: rating.KindCorrection, MeterCode: k, AmountMinor: d}
			if k != "" {
				desc = fmt.Sprintf("%s: price change", k)
				l.UnitLabel = meters[k].Unit
				if nl, ok := newLine[k]; ok {
					l.Quantity = nl.Quantity
				} else if q := quantityOf(usage[cid], k); q != nil {
					l.Quantity = q
				}
			}
			l.Description = desc
			deltas = append(deltas, l)
		}
		if total == 0 {
			continue
		}
		cuid, err := uuid.Parse(cid)
		if err != nil {
			return permanent{err}
		}
		if err := m.recordRerating(ctx, job, cuid, snap, tn.GetTenant().GetCurrency(), deltas, total, meters); err != nil {
			return err
		}
	}

	// Finalize the correction invoices: number, open, saga. Finalizing twice is a no-op.
	rows, err := m.cfg.Pool.Query(ctx, `SELECT id FROM invoices WHERE tenant_id = $1 AND rerating_job_id = $2 AND status = 'draft' ORDER BY id`, tid, job.ID)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := m.Finalize(ctx, tid, id); err != nil {
			return err
		}
	}

	_, err = m.cfg.Pool.Exec(ctx, `
		UPDATE rerating_jobs SET status = 'COMPLETED', completed_at = $3, last_error = NULL, customers_rated = $4,
		  corrections = (SELECT count(*) FROM invoices WHERE tenant_id = $1 AND rerating_job_id = $2),
		  correction_minor = COALESCE((SELECT sum(total_minor) FROM invoices WHERE tenant_id = $1 AND rerating_job_id = $2), 0),
		  credits = (SELECT count(*) FROM credit_balances WHERE tenant_id = $1 AND source_rerating_job_id = $2),
		  credit_minor = COALESCE((SELECT sum(amount_minor + 0) FROM credit_balances WHERE tenant_id = $1 AND source_rerating_job_id = $2), 0)
		WHERE tenant_id = $1 AND id = $2`, tid, job.ID, m.cfg.Now(), rated)
	return err
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
		if total < 0 {
			_, err := tx.Exec(ctx, `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_rerating_job_id)
				VALUES ($1,$2,$3,$4,$5) ON CONFLICT (tenant_id, customer_id, source_rerating_job_id) DO NOTHING`,
				job.TenantID, customer, uuid.New(), -total, job.ID)
			return err
		}
		id := uuid.New()
		var got uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, rerating_job_id)
			VALUES ($1,$2,$3,$4,'CORRECTION',$5,$6,$7,$7,'draft',$8,$9) ON CONFLICT DO NOTHING RETURNING id`,
			job.TenantID, id, customer, job.PeriodID, job.PlanVersionID, currency, total, snapshot, job.ID).Scan(&got)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // this job already corrected this customer
		}
		if err != nil {
			return err
		}
		for i, l := range deltas {
			var qty, scale, label, meter any
			if l.MeterCode != "" {
				meter = l.MeterCode
				if l.Quantity != nil {
					units, err := unitsOf(l.Quantity, meters[l.MeterCode].Scale)
					if err != nil {
						return err
					}
					qty, scale, label = units, int16(meters[l.MeterCode].Scale), l.UnitLabel //nolint:gosec // 0..6
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, meter_code, quantity, quantity_scale, unit_label, amount_minor, description)
				VALUES ($1,$2,$3,'CORRECTION',$4,$5::numeric,$6,$7,$8,$9)`,
				job.TenantID, id, i+1, meter, qty, scale, label, l.AmountMinor, l.Description+" ("+job.PeriodID+")"); err != nil {
				return err
			}
		}
		return nil
	})
}
