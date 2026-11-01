// Package command is the invoice saga engine: it applies the transition table (sagas/model) to invoice_sagas rows and writes the commands, the
// facts and the tenant events each transition asks for, all in the caller's transaction, plus the operator's retry and void.
package command

import (
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/env"
)

// MaxStepAttempts is how often the sweeper issues the command of one step before giving up.
const MaxStepAttempts = 3

// DefaultDunningAttempts is the number of payment retries after the first declined charge for a tenant that set no delays of its own (it may set up to six): the
// number of delays PAYRUDDER_DUNNING_LIVE and PAYRUDDER_DUNNING_SANDBOX hold.
const DefaultDunningAttempts = 3

// InvoicesTopic is the Kafka topic of InvoiceFact events.
const InvoicesTopic = "billing.invoices.v1"

var (
	commandNS = uuid.MustParse("c0a1d7f2-3e57-5b6e-8d13-9a4f0b7c2e41")
	factNS    = uuid.MustParse("7d6b2a90-14c8-5e0f-a8b2-5c3e91f4d0aa")
)

// Config tunes an Engine.
type Config struct {
	Now func() time.Time
	// StepTimeout is how long a command may stay unanswered before the sweeper re-issues it (default 2 minutes).
	StepTimeout time.Duration
	// Dunning are the delays before payment retry 1..3, per environment.
	DunningLive    []time.Duration
	DunningSandbox []time.Duration
	// TransferPoll is how often the status of a payment that settles later (a bank transfer) is asked for (default 6 hours). A receipt an operator confirms
	// does not wait for it: payments-worker nudges the saga.
	TransferPoll time.Duration
	// NotFoundGrace is how long after a charge was first sent a "not found" status answer is not trusted: the status is asked again instead of charging again
	// (default 10 minutes, default (not agreed)). NotFoundGraceSandbox is the same for sandbox tenants, on seconds like their dunning (ADR 0011).
	NotFoundGrace, NotFoundGraceSandbox time.Duration
	// KeyHorizon is how long a provider is taken to remember an idempotency key: a "not found" answer for a charge older than that parks the saga instead of
	// charging again under the same key (default 24 hours, Stripe's retention in docs/stripe-compatibility-matrix.md, default (not agreed); iyzico's is UNKNOWN).
	KeyHorizon time.Duration
	Log        *slog.Logger
	// Tenants, when set, is asked for the environment of the saga's tenant before a command goes out (see publishCommand).
	Tenants *env.Directory
}

// Defaults of the "not found" window of a charge (Config.NotFoundGrace, NotFoundGraceSandbox, KeyHorizon).
const (
	DefaultNotFoundGrace        = 10 * time.Minute
	DefaultNotFoundGraceSandbox = 10 * time.Second
	DefaultKeyHorizon           = 24 * time.Hour
)

// DefaultTransferPoll is how often a pending bank transfer is looked for in the ledger when nobody has nudged the saga.
const DefaultTransferPoll = 6 * time.Hour

// DefaultDunningLive is +1 d, +3 d, +7 d; DefaultDunningSandbox compresses them to minutes (ADR 0011).
var (
	DefaultDunningLive    = []time.Duration{24 * time.Hour, 72 * time.Hour, 168 * time.Hour}
	DefaultDunningSandbox = []time.Duration{time.Minute, 3 * time.Minute, 7 * time.Minute}
)

// Engine applies the transition table to invoice_sagas rows. Every method runs inside the caller's transaction.
type Engine struct {
	cfg Config

	transitions metric.Int64Counter
	ignored     metric.Int64Counter
}

// NewEngine applies defaults and registers metrics.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.StepTimeout <= 0 {
		cfg.StepTimeout = 2 * time.Minute
	}
	if len(cfg.DunningLive) == 0 {
		cfg.DunningLive = DefaultDunningLive
	}
	if cfg.TransferPoll <= 0 {
		cfg.TransferPoll = DefaultTransferPoll
	}
	if len(cfg.DunningSandbox) == 0 {
		cfg.DunningSandbox = DefaultDunningSandbox
	}
	if cfg.NotFoundGrace <= 0 {
		cfg.NotFoundGrace = DefaultNotFoundGrace
	}
	if cfg.NotFoundGraceSandbox <= 0 {
		cfg.NotFoundGraceSandbox = DefaultNotFoundGraceSandbox
	}
	if cfg.KeyHorizon <= 0 {
		cfg.KeyHorizon = DefaultKeyHorizon
	}
	if max(cfg.NotFoundGrace, cfg.NotFoundGraceSandbox) >= cfg.KeyHorizon {
		return nil, fmt.Errorf("saga: the not-found grace period (%s, sandbox %s) must be shorter than the key horizon (%s)", cfg.NotFoundGrace, cfg.NotFoundGraceSandbox, cfg.KeyHorizon)
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	m := otel.Meter("github.com/0furkancolak/payrudder/services/billing-svc/saga")
	e := &Engine{cfg: cfg}
	var err error
	if e.transitions, err = m.Int64Counter("saga_transitions", metric.WithDescription("Saga transitions applied, by from, to and cause")); err != nil {
		return nil, err
	}
	if e.ignored, err = m.Int64Counter("billing_saga_events_ignored", metric.WithDescription("Replies and triggers that were invalid or stale for the saga's state")); err != nil {
		return nil, err
	}
	return e, nil
}

// DunningDefaults are the delays before payment retry 1..n of a tenant that set none: the configured ones for its environment, not the built-in ones.
func (e *Engine) DunningDefaults(sandbox bool) []time.Duration {
	if sandbox {
		return slices.Clone(e.cfg.DunningSandbox)
	}
	return slices.Clone(e.cfg.DunningLive)
}

// notFoundGrace is the grace period of the saga's tenant: sandbox tenants run it on seconds.
func (e *Engine) notFoundGrace(s *saga) time.Duration {
	if s.Snap.Environment == env.Sandbox.String() {
		return e.cfg.NotFoundGraceSandbox
	}
	return e.cfg.NotFoundGrace
}

// Now exposes the engine's clock (a test clock in tests).
func (e *Engine) Now() time.Time { return e.cfg.Now() }
