package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Job states.
const (
	StateQueued  = "queued"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

// Settings of a Runner. The zero value of each field is replaced by its default.
type Settings struct {
	// Poll is how often an idle runner looks for a job (default 3 s).
	Poll time.Duration
	// Lease is how long a running job belongs to its runner without a sign of life; after it another replica takes the job over (default 5 min). Each finished step
	// renews it.
	Lease time.Duration
	// Retention is the idle time after which a sandbox is reset by the sweep; zero or negative switches the sweep off.
	Retention time.Duration
	// SweepEvery is how often the sweep looks (default 1 h).
	SweepEvery time.Duration
	// SweepBatch bounds the resets one sweep starts (default 50).
	SweepBatch int
}

func (s Settings) withDefaults() Settings {
	if s.Poll <= 0 {
		s.Poll = 3 * time.Second
	}
	if s.Lease <= 0 {
		s.Lease = 5 * time.Minute
	}
	if s.SweepEvery <= 0 {
		s.SweepEvery = time.Hour
	}
	if s.SweepBatch <= 0 {
		s.SweepBatch = 50
	}
	return s
}

// Runner executes reset jobs. Any number of replicas can run: a job is claimed with FOR UPDATE SKIP LOCKED, so one replica works on it at a time, and a job whose runner
// died is taken over when its lease runs out. Every step is idempotent, so a takeover starts the steps again.
type Runner struct {
	pool   *pgxpool.Pool
	places *placement.Places // where the tenants' own rows are; nil (or cells off) is the pooled database
	steps  []Step
	set    Settings
	log    *slog.Logger
	now    func() time.Time
}

// NewRunner returns a runner on the system pool. steps are run in order: plans-svc's own first.
func NewRunner(systemPool *pgxpool.Pool, steps []Step, set Settings, log *slog.Logger) *Runner {
	return &Runner{pool: systemPool, steps: steps, set: set.withDefaults(), log: log, now: time.Now}
}

// WithPlaces tells the runner where the tenants' own rows are, so that the retention sweep looks for the last customer or subscription of a sandbox in a cell in its
// schema (ADR 0018).
func (r *Runner) WithPlaces(p *placement.Places) *Runner {
	r.places = p
	return r
}

// Run works until ctx ends: jobs, and now and then the retention sweep.
func (r *Runner) Run(ctx context.Context) error {
	poll := time.NewTicker(r.set.Poll)
	defer poll.Stop()
	sweep := time.NewTicker(r.set.SweepEvery)
	defer sweep.Stop()
	for {
		// Drain what is queued before waiting again.
		for {
			ran, err := r.RunOnce(ctx)
			if err != nil {
				r.log.ErrorContext(ctx, "sandbox reset: runner", "err", err)
			}
			if !ran || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-sweep.C:
			if n, err := r.Sweep(ctx); err != nil {
				r.log.ErrorContext(ctx, "sandbox reset: retention sweep", "err", err)
			} else if n > 0 {
				r.log.InfoContext(ctx, "sandbox reset: idle sandboxes queued", "count", n)
			}
		case <-poll.C:
		}
	}
}

// RunOnce claims one job and runs it; ran is false when there was none.
func (r *Runner) RunOnce(ctx context.Context) (ran bool, err error) {
	job, err := sqlc.New(r.pool).ClaimSandboxReset(ctx, interval(r.set.Lease))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	r.execute(ctx, job)
	return true, nil
}

func interval(d time.Duration) pgtype.Interval { return convert.Interval(d) }

// execute runs the steps of a job in order and records each. The first failure stops the job (the steps after it do not run: a later step's data may depend on the
// earlier one being gone); starting a new reset repeats everything, which is safe.
func (r *Runner) execute(ctx context.Context, job sqlc.SandboxReset) {
	db := sqlc.New(r.pool)
	states := make([]stepState, len(r.steps))
	for i, s := range r.steps {
		states[i] = stepState{Name: s.Name, State: "pending"}
	}
	finish := func(state, msg string) {
		raw, _ := json.Marshal(states)
		// Not the job's ctx: a runner that is shutting down still writes down where it got to.
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := db.FinishSandboxReset(wctx, sqlc.FinishSandboxResetParams{ID: job.ID, State: state, Error: msg, Steps: raw}); err != nil {
			r.log.ErrorContext(ctx, "sandbox reset: result not recorded", "reset_id", job.ID, "err", err)
		}
	}
	for i, s := range r.steps {
		if s.Run == nil {
			states[i].State = "skipped"
			continue
		}
		deleted, err := s.Run(ctx, job.TenantID)
		if err != nil {
			states[i].State, states[i].Error = "failed", err.Error()
			r.log.WarnContext(ctx, "sandbox reset: step failed", "reset_id", job.ID, "tenant_id", job.TenantID, "step", s.Name, "err", err)
			finish(StateFailed, s.Name+": "+err.Error())
			return
		}
		states[i].State, states[i].Deleted = "done", deleted
		raw, _ := json.Marshal(states)
		if err := db.SaveSandboxResetSteps(ctx, sqlc.SaveSandboxResetStepsParams{ID: job.ID, Steps: raw, Lease: interval(r.set.Lease)}); err != nil {
			r.log.WarnContext(ctx, "sandbox reset: progress not recorded", "reset_id", job.ID, "err", err)
		}
	}
	r.log.InfoContext(ctx, "sandbox reset done", "reset_id", job.ID, "tenant_id", job.TenantID, "kind", job.Kind)
	finish(StateDone, "")
}

// Sweep queues a reset for every sandbox that nobody has touched for the retention period (key use, customers or subscriptions made, a reset). It returns how many.
// Several replicas sweeping at once queue each sandbox once: a sandbox with a reset open is not idle, and the table allows one open reset per sandbox.
func (r *Runner) Sweep(ctx context.Context) (int, error) {
	if r.set.Retention <= 0 {
		return 0, nil
	}
	db := sqlc.New(r.pool)
	idle, err := db.IdleSandboxTenants(ctx, sqlc.IdleSandboxTenantsParams{Idle: interval(r.set.Retention), Batch: int32(r.set.SweepBatch)}) //nolint:gosec // bounded setting
	if err != nil {
		return 0, err
	}
	queued := 0
	queue := func(appID, tenantID uuid.UUID) error {
		_, err := db.InsertSandboxReset(ctx, sqlc.InsertSandboxResetParams{ID: uuid.New(), AppID: appID, TenantID: tenantID, Kind: "retention", Steps: Initial(Names(r.steps))})
		switch {
		case pgxx.ErrCode(err) == pgxx.CodeUniqueViolation:
			// Another replica was first.
		case err != nil:
			return err
		default:
			queued++
		}
		return nil
	}
	for _, t := range idle {
		if err := queue(t.AppID, t.TenantID); err != nil {
			return queued, err
		}
	}
	if r.places.CellsOn() && queued < r.set.SweepBatch {
		if err := r.sweepCells(ctx, db, queue, r.set.SweepBatch-queued); err != nil {
			return queued, err
		}
	}
	return queued, nil
}

// sweepCells queues the idle sandboxes that are in cells, at most room of them. Their pooled signs of life are read a page at a time (creation order, after a
// cursor), and each candidate is asked in its own schema for the last customer or subscription made, so that a sandbox with recent customers is not reset because
// the pooled tables know nothing of them. The candidates that turn out to be busy are skipped, not counted: the sweep goes on to the next page.
func (r *Runner) sweepCells(ctx context.Context, db *sqlc.Queries, queue func(appID, tenantID uuid.UUID) error, room int) error {
	idle := r.now().Add(-r.set.Retention)
	afterAt, afterID := time.Unix(0, 0), uuid.Nil
	for room > 0 {
		page, err := db.IdleSandboxCellCandidates(ctx, sqlc.IdleSandboxCellCandidatesParams{
			Idle: interval(r.set.Retention), AfterAt: afterAt, AfterID: afterID, PageSize: int32(sweepPage)}) //nolint:gosec // a constant
		if err != nil {
			return err
		}
		for _, c := range page {
			var busy time.Time
			err := r.places.Read(ctx, c.TenantID, func(q *sqlc.Queries) (err error) {
				busy, err = q.SandboxTenantActivity(ctx, c.TenantID)
				return err
			})
			switch {
			case errors.Is(err, tenantdir.ErrNotServing):
				continue // moving: nobody's to reset now
			case err != nil:
				return err
			}
			if busy.After(idle) {
				continue
			}
			if err := queue(c.AppID, c.TenantID); err != nil {
				return err
			}
			if room--; room == 0 {
				return nil
			}
		}
		if len(page) < sweepPage {
			return nil
		}
		last := page[len(page)-1]
		afterAt, afterID = last.CreatedAt, last.TenantID
	}
	return nil
}

// sweepPage is how many candidates in cells the sweep reads at a time.
const sweepPage = 100
