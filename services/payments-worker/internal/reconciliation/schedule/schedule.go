// Package schedule runs the reconciliation on a timer (docs/plans/22 G7): for every tenant that has taken a live payment, the current and the previous billing period,
// keeping what it finds (command.Run). A run is idempotent and takes a lock per tenant and period, so a second replica, or an operator's run beside it, only repeats work.
package schedule

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/command"
)

// lastPass is reconciliation_last_pass_timestamp_seconds, read on every scrape: when the last pass of this process finished. A pass that could not list the tenants, or was cut
// short by the process stopping, does not count; a pass that visited nobody does (there was nothing to reconcile). ReconciliationRunStalled reads it, so a schedule that hangs
// (a stuck database call, a provider that never answers) is seen even when no run fails. Without a pass yet, or with the schedule off, there is no series.
var lastPass = func() *atomic.Int64 {
	var at atomic.Int64
	m := otel.Meter("github.com/0furkancolak/payrudder/services/payments-worker/reconciliation")
	g, err := m.Int64ObservableGauge("reconciliation_last_pass_timestamp_seconds", metric.WithDescription("Unix time at which the last scheduled reconciliation pass of this process finished"))
	if err == nil {
		_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
			if v := at.Load(); v != 0 {
				o.ObserveInt64(g, v)
			}
			return nil
		}, g)
	}
	if err != nil {
		panic(err)
	}
	return &at
}()

// Job is the scheduled reconciliation.
type Job struct {
	Deps command.Deps
	// Interval is the pause between passes. A pass that takes longer than the interval is followed at once by the next.
	Interval time.Duration
	Log      *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Periods are the billing periods a pass looks at: the one that is running and the one before it, because a payment of the last days of a month is settled and
// reported in the next, and an invoice is paid after its period closed.
func Periods(now time.Time) []string {
	now = now.UTC()
	return []string{now.Format("2006-01"), now.AddDate(0, -1, 0).Format("2006-01")}
}

// Run passes until ctx ends: once at the start, then every Interval.
func (j *Job) Run(ctx context.Context) error {
	if j.Interval <= 0 {
		return errors.New("schedule: no interval")
	}
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		j.Pass(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Pass runs the reconciliation for every tenant that has taken a live payment. A tenant whose run fails does not stop the others; the failure is logged and recorded in
// the run's own row.
func (j *Job) Pass(ctx context.Context) {
	now := time.Now()
	if j.Now != nil {
		now = j.Now()
	}
	tenants, err := j.Deps.Discrepancies.LiveTenants(ctx)
	if err != nil {
		j.Log.WarnContext(ctx, "scheduled reconciliation: the tenants could not be listed", "err", err)
	}
	for _, t := range tenants {
		for _, period := range Periods(now) {
			if ctx.Err() != nil {
				return
			}
			j.one(ctx, t, period)
		}
	}
	if err == nil && ctx.Err() == nil {
		done := time.Now()
		if j.Now != nil {
			done = j.Now()
		}
		lastPass.Store(done.Unix())
	}
}

func (j *Job) one(ctx context.Context, t uuid.UUID, period string) {
	res, err := command.Run(ctx, j.Deps, command.RunInput{Tenant: t, Environment: "live", Period: period, StartedBy: command.BySchedule, Actor: "system"})
	switch {
	case errors.Is(err, tenantdir.ErrNotServing):
		// moving: the next pass finds it
	case err != nil:
		j.Log.WarnContext(ctx, "scheduled reconciliation failed", "tenant", t, "period", period, "err", err)
	case res.Opened+res.Reopened > 0:
		j.Log.InfoContext(ctx, "scheduled reconciliation found discrepancies", "tenant", t, "period", period, "opened", res.Opened, "reopened", res.Reopened, "cleared", res.Cleared)
	}
}
