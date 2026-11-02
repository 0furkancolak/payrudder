// Package health is the runtime health of provider accounts (docs/plans/22 G8, docs/provider-health.md): a circuit breaker per account that makes a new charge fail fast
// while the provider is down, and a periodic check of every account that keeps its state where a person can see it. It never decides where a charge goes instead:
// that is the handler's, under the rule that a timeout never fails over.
package health

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Ref names an account.
type Ref = store.AccountRef

var (
	meter       = otel.Meter("github.com/0furkancolak/payrudder/services/payments-worker/health")
	transitions = counter("provider_breaker_transitions", "Circuit breaker changes of provider accounts, by provider and the state they changed to")
	rejections  = counter("provider_breaker_rejections", "Charges refused without a call because the account's circuit was open, by provider")
	checks      = counter("provider_health_checks", "Periodic health checks of provider accounts, by provider and result")
)

func counter(name, desc string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		panic(err)
	}
	return c
}

// Breakers holds the circuit breaker of every account a worker has charged through, made on first use. A breaker counts in this process's memory and a restart begins
// closed; with Shared set, replicas also share whether the circuit is open (see payments.SharedBreaker). What it persists in the database is for people to read
// (store.ProviderHealth), not for the next process to start from.
type Breakers struct {
	// Settings are the numbers of every breaker (payments.DefaultBreaker where zero).
	Settings payments.BreakerSettings
	// Shared, when set (Redis), makes every account's circuit one for all replicas; nil keeps each replica's breaker its own.
	Shared SharedStates
	// Store, when its DB is set, keeps the state of a breaker when it changes.
	Store store.ProviderHealth
	Log   *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu sync.Mutex
	m  map[Ref]*payments.Breaker
}

// For returns the breaker of an account.
func (b *Breakers) For(ref Ref) *payments.Breaker {
	b.mu.Lock()
	defer b.mu.Unlock()
	if br, ok := b.m[ref]; ok {
		return br
	}
	if b.m == nil {
		b.m = map[Ref]*payments.Breaker{}
	}
	br := payments.NewBreaker(b.Settings, b.Now, func(to payments.BreakerState, failures int) { b.changed(ref, to, failures) })
	if b.Shared != nil {
		br.Shared = b.Shared.For(ref)
	}
	b.m[ref] = br
	return br
}

// Allow asks the account's breaker whether a charge may go to its provider; see payments.Breaker.Allow. A refusal is counted.
func (b *Breakers) Allow(ref Ref) (func(error), error) {
	done, err := b.For(ref).Allow()
	if err != nil {
		rejections.Add(context.Background(), 1, metric.WithAttributes(attribute.String("provider", payments.BaseProvider(ref.Provider))))
	}
	return done, err
}

// changed counts and keeps a change of state. Keeping it is best effort: the state is for people to read, and a charge must not fail because it could not be written.
func (b *Breakers) changed(ref Ref, to payments.BreakerState, failures int) {
	transitions.Add(context.Background(), 1, metric.WithAttributes(attribute.String("provider", payments.BaseProvider(ref.Provider)), attribute.String("to", string(to))))
	if b.Log != nil {
		b.Log.Warn("provider account circuit changed", "tenant", ref.Tenant, "provider", ref.Provider, "environment", ref.Environment, "to", string(to), "failures", failures)
	}
	if b.Store.DB == nil {
		return
	}
	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}
	var opened, failed *time.Time
	lastError := ""
	if to != payments.BreakerClosed {
		opened, failed, lastError = &now, &now, "circuit_open"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.Store.PutBreaker(ctx, ref, string(to), int64(failures), lastError, opened, failed); err != nil && b.Log != nil {
		b.Log.Warn("provider account circuit not recorded", "tenant", ref.Tenant, "provider", ref.Provider, "err", err)
	}
}

// Monitor checks every provider account periodically with the adapter's own health call (payments.HealthChecker: one authenticated, read-only request that moves no money)
// and keeps what it finds. The outcome also goes to the account's breaker, so a provider that came back closes the circuit before a charge has to probe it, and one that is down
// opens it without a charge having to fail first.
type Monitor struct {
	Store    store.ProviderHealth
	Resolver target.Resolver
	Breakers *Breakers
	// Interval is the pause between passes and Timeout the limit of one check.
	Interval, Timeout time.Duration
	Log               *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Run passes until ctx ends: once at the start, then every Interval.
func (m *Monitor) Run(ctx context.Context) error {
	if m.Interval <= 0 {
		return errors.New("health: no interval")
	}
	t := time.NewTicker(m.Interval)
	defer t.Stop()
	for {
		m.Pass(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Pass checks every account once, one after the other. An account that cannot be checked is recorded and does not stop the others.
func (m *Monitor) Pass(ctx context.Context) {
	refs, err := m.Store.Accounts(ctx)
	if err != nil {
		m.Log.WarnContext(ctx, "health check: the accounts could not be listed", "err", err)
	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
		m.Check(ctx, ref)
	}
}

// Check checks one account and keeps the result.
func (m *Monitor) Check(ctx context.Context, ref Ref) {
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	status, class, outcome := store.HealthUnknown, "", error(nil)
	p, err := m.Resolver.Named(ctx, ref.Tenant, ref.Provider, ref.Environment)
	if err != nil {
		class = "unusable" // no adapter can be built from what is stored (or the account was just removed)
	} else {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		h, err := payments.Check(cctx, p)
		cancel()
		switch {
		case err == nil && h.Remote:
			status = store.HealthHealthy
		case err == nil:
			class = "no_remote_check" // the adapter has no call to ask its provider with (a bank transfer)
		case errors.Is(err, payments.ErrUnreachable):
			status, class, outcome = store.HealthDown, "unreachable", payments.ErrUnreachable
		default:
			status, class, outcome = store.HealthBadCredentials, "bad_credentials", err
		}
	}
	result := status
	if class != "" {
		result = class
	}
	checks.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", payments.BaseProvider(ref.Provider)), attribute.String("result", result)))
	if status != store.HealthUnknown {
		m.Breakers.For(ref).Observe(outcome) // an answer, even a refusal of the credentials, is a provider that is up; unreachable is an outage
	}
	var ok, failed *time.Time
	switch status {
	case store.HealthHealthy:
		ok = &now
	case store.HealthDown, store.HealthBadCredentials:
		failed = &now
	}
	if err := m.Store.PutCheck(ctx, ref, status, class, now, ok, failed); err != nil {
		m.Log.WarnContext(ctx, "health check not recorded", "tenant", ref.Tenant, "provider", ref.Provider, "err", err)
	}
}
