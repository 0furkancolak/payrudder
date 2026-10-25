// Package command is the invoice saga engine: it applies the transition table (sagas/model) to invoice_sagas rows and writes the commands, the
// facts and the tenant events each transition asks for, all in the caller's transaction, plus the operator's retry and void.
package command

import (
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/env"
)

// MaxStepAttempts is how often the sweeper issues the command of one step before giving up.
const MaxStepAttempts = 3

// MaxDunning is the number of payment retries after the first declined charge.
const MaxDunning = 3

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
	Log          *slog.Logger
	// Tenants, when set, is asked for the environment of the saga's tenant before a command goes out (see publishCommand).
	Tenants *env.Directory
}

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

// Now exposes the engine's clock (a test clock in tests).
func (e *Engine) Now() time.Time { return e.cfg.Now() }
