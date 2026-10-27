// Package command is the trial-ending reminder: a job over all tenants that finds the trialing subscriptions whose trial ends within the window and that were
// not reminded of this end yet, and for each one, in its tenant's transaction, records the notice, queues the subscription.trial_will_end webhook event and
// writes the fact the notifier turns into the customer's e-mail (the outbox, relayed to subscription.trial-will-end.v1).
package command

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
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
type Job struct {
	system, app *pgxpool.Pool
	window      time.Duration
	every       time.Duration
	batch       int
	now         func() time.Time
	log         *slog.Logger
}

// New returns the job.
func New(systemPool, appPool *pgxpool.Pool, cfg Config, log *slog.Logger) *Job {
	if cfg.Every <= 0 {
		cfg.Every = 10 * time.Minute
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 200
	}
	return &Job{system: systemPool, app: appPool, window: model.Window(cfg.Days), every: cfg.Every, batch: cfg.Batch, now: time.Now, log: log}
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
	return reminded, len(due), nil
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
