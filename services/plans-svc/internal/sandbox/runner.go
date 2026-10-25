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
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
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
	pool  *pgxpool.Pool
	steps []Step
	set   Settings
	log   *slog.Logger
}

// NewRunner returns a runner on the system pool. steps are run in order: plans-svc's own first.
func NewRunner(systemPool *pgxpool.Pool, steps []Step, set Settings, log *slog.Logger) *Runner {
	return &Runner{pool: systemPool, steps: steps, set: set.withDefaults(), log: log}
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
	for _, t := range idle {
		_, err := db.InsertSandboxReset(ctx, sqlc.InsertSandboxResetParams{ID: uuid.New(), AppID: t.AppID, TenantID: t.TenantID, Kind: "retention", Steps: Initial(Names(r.steps))})
		switch {
		case pgxx.ErrCode(err) == pgxx.CodeUniqueViolation:
			// Another replica was first.
		case err != nil:
			return queued, err
		default:
			queued++
		}
	}
	return queued, nil
}
