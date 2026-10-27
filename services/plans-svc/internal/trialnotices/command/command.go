// Package command is the trial-ending reminder: a job over all tenants that finds the trialing subscriptions whose trial ends within the window and that were
// not reminded of this end yet, and for each one, in its tenant's transaction, records the notice, queues the subscription.trial_will_end webhook event and
// writes the fact the notifier turns into the customer's e-mail (the outbox, relayed to subscription.trial-will-end.v1).
package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/trialnotices/model"
	webhookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// Config sets the job; the zero value of each field is replaced by its default.
type Config struct {
	// Days is how many days before its end a trial is reminded (default model.DefaultDays).
	Days int
	// Every is how often the job looks (default 10 minutes; a reminder is days ahead, so the minute does not matter).
	Every time.Duration
	// Batch bounds the trials one pass takes (default 200).
	Batch int
}

// Job reminds trials that are about to end. Finding them reads across tenants on the system pool; recording and emitting happens on the app pool, in the tenant's
// own transaction, so a webhook event and a notice are in the same tenant's rows or in none. It is safe to run twice, and on several instances at once: the
// notice's primary key lets one of them in, and the other finds the notice and does nothing.
//
// With cells on (ADR 0018) the tenants in the pooled database are listed in one statement as ever, and each tenant placed in a cell is listed in its own schema, in a
// transaction of its own: one list, one tenant. Each of them gets a batch of its own, so a tenant whose trials keep failing cannot keep the others waiting.
type Job struct {
	system  *pgxpool.Pool
	app     pgxx.Beginner
	places  *placement.Places
	window  time.Duration
	every   time.Duration
	batch   int
	now     func() time.Time
	log     *slog.Logger
	visited func(uuid.UUID) // tests: sees every tenant in a cell the job visits
}

// New returns the job. appDB is the app pool, or the app-role router (placement.AppDB) when cells are on.
func New(systemPool *pgxpool.Pool, appDB pgxx.Beginner, cfg Config, log *slog.Logger) *Job {
	if cfg.Every <= 0 {
		cfg.Every = 10 * time.Minute
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 200
	}
	return &Job{system: systemPool, app: appDB, window: model.Window(cfg.Days), every: cfg.Every, batch: cfg.Batch, now: time.Now, log: log}
}

// WithPlaces tells the job where the tenants' own rows are.
func (j *Job) WithPlaces(p *placement.Places) *Job {
	j.places = p
	return j
}

// WithVisits makes the job report each tenant in a cell it visits to fn (tests).
func (j *Job) WithVisits(fn func(uuid.UUID)) *Job {
	j.visited = fn
	return j
}

// WithClock sets the clock the job reads (tests).
func (j *Job) WithClock(now func() time.Time) *Job {
	j.now = now
	return j
}

// Once reminds the trials that are due at now, at most one batch, and says how many it reminded and how many it looked at. A trial that fails is logged and left
// for the next pass; the others go on.
func (j *Job) Once(ctx context.Context) (reminded, looked int, err error) {
	now := j.now()
	due, err := sqlc.New(j.system).SystemListTrialsEndingSoon(ctx, sqlc.SystemListTrialsEndingSoonParams{
		Owner: domain.OwnerPayrudder, FromTime: now, Until: now.Add(j.window), Batch: int32(j.batch), //nolint:gosec // a small config value
	})
	if err != nil {
		return 0, 0, err
	}
	reminded, looked = j.remindAll(ctx, now, due)
	err = j.places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		if j.visited != nil {
			j.visited(t.TenantID)
		}
		var due []sqlc.SystemListTrialsEndingSoonRow
		err := j.places.CellTx(ctx, t, func(tx pgx.Tx) (err error) {
			due, err = sqlc.New(tx).SystemListTrialsEndingSoon(ctx, sqlc.SystemListTrialsEndingSoonParams{
				Owner: domain.OwnerPayrudder, FromTime: now, Until: now.Add(j.window), Batch: int32(j.batch), //nolint:gosec // a small config value
			})
			return err
		})
		if err != nil {
			return err
		}
		for _, d := range due {
			if d.TenantID != t.TenantID {
				return fmt.Errorf("trial reminder: subscription %s of tenant %s is in the schema of tenant %s", d.ID, d.TenantID, t.TenantID)
			}
		}
		r, l := j.remindAll(ctx, now, due)
		reminded, looked = reminded+r, looked+l
		return nil
	})
	return reminded, looked, err
}

// remindAll reminds the trials of a list and says how many it reminded and how many it looked at.
func (j *Job) remindAll(ctx context.Context, now time.Time, due []sqlc.SystemListTrialsEndingSoonRow) (reminded, looked int) {
	for _, d := range due {
		ok, err := j.remind(ctx, now, d)
		if err != nil {
			j.log.WarnContext(ctx, "trial reminder: could not remind", "tenant_id", d.TenantID, "subscription_id", d.ID, "error", err)
			continue
		}
		if ok {
			reminded++
		}
	}
	return reminded, len(due)
}

// remind records the notice of one trial and emits its event and fact, or does nothing when the trial is not due any more or was reminded already. It says
// whether it reminded.
func (j *Job) remind(ctx context.Context, now time.Time, d sqlc.SystemListTrialsEndingSoonRow) (bool, error) {
	done := false
	err := pgxx.InTenantTx(ctx, j.app, d.TenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		// The row is locked first: a plan change, a pause or a cancellation waits for this transaction, and a second instance waits here, then finds the notice.
		row, err := subsqry.Lock(ctx, q, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		due := model.Trial{State: row.State, OpenEnded: row.OpenEnded, CancelAtPeriodEnd: row.CancelAtPeriodEnd, Owner: row.Owner, EndsAt: row.TrialEndsAt}
		// The trial was changed since the list was read: another pass lists it again under its new end.
		if !due.Remindable(now, j.window) || !row.TrialEndsAt.Equal(d.TrialEndsAt) {
			return nil
		}
		n, err := q.InsertTrialNotice(ctx, sqlc.InsertTrialNoticeParams{
			CustomerID: row.CustomerID, TrialEndsAt: d.TrialEndsAt, SubscriptionID: row.ID, NotifiedAt: now,
		})
		if err != nil || n == 0 {
			return err
		}
		if err := webhookscmd.EmitSubscription(ctx, q, now, webhooksmodel.SubscriptionTrialWillEnd, convert.SubscriptionOf(row, now), nil); err != nil {
			return err
		}
		payload, err := proto.Marshal(model.Event(d.TenantID, row.CustomerID, row.ID, row.PlanCode, d.TrialEndsAt, now))
		if err != nil {
			return err
		}
		if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: model.Topic, PartitionKey: model.PartitionKey(d.TenantID, row.CustomerID), Payload: payload}); err != nil {
			return err
		}
		done = true
		return q.NotifyOutbox(ctx)
	})
	return done && err == nil, err
}

// Run looks now and then every interval until ctx ends. A pass that fills a whole batch runs again at once.
func (j *Job) Run(ctx context.Context) error {
	t := time.NewTicker(j.every)
	defer t.Stop()
	for {
		for {
			n, looked, err := j.Once(ctx)
			if err != nil {
				j.log.ErrorContext(ctx, "trial reminder: listing the trials failed", "error", err)
			} else if n > 0 {
				j.log.InfoContext(ctx, "trial reminders recorded", "count", n)
			}
			// Another pass only when this one made progress through a full batch: trials that keep failing must not make it spin.
			if err != nil || n == 0 || looked < j.batch || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
