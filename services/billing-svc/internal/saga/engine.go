package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	eventsv1 "github.com/0furkancolak/quotify/gen/go/quotify/events/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/outbox"
	"github.com/0furkancolak/quotify/pkg/quantity"
	"github.com/0furkancolak/quotify/pkg/rabbitx"
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

// Snapshot is what the saga's commands need about the customer and the tenant, frozen when the draft was created.
type Snapshot struct {
	CustomerExternalID string `json:"customer_external_id"`
	CustomerName       string `json:"customer_name"`
	CustomerEmail      string `json:"customer_email"`
	PaymentMethod      string `json:"payment_method"`
	TenantName         string `json:"tenant_name"`
	Environment        string `json:"environment"` // "sandbox" | "live"
	CurrencyExponent   int    `json:"currency_exponent"`
	LogoObjectKey      string `json:"logo_object_key,omitempty"`
}

// Config tunes an Engine.
type Config struct {
	Now func() time.Time
	// StepTimeout is how long a command may stay unanswered before the sweeper re-issues it (default 2 minutes).
	StepTimeout time.Duration
	// Dunning are the delays before payment retry 1..3, per environment.
	DunningLive    []time.Duration
	DunningSandbox []time.Duration
	Log            *slog.Logger
}

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
	if len(cfg.DunningSandbox) == 0 {
		cfg.DunningSandbox = DefaultDunningSandbox
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	m := otel.Meter("github.com/0furkancolak/quotify/services/billing-svc/saga")
	e := &Engine{cfg: cfg}
	var err error
	if e.transitions, err = m.Int64Counter("billing_saga_transitions", metric.WithDescription("Saga transitions applied, by from, to and cause")); err != nil {
		return nil, err
	}
	if e.ignored, err = m.Int64Counter("billing_saga_events_ignored", metric.WithDescription("Replies and triggers that were invalid or stale for the saga's state")); err != nil {
		return nil, err
	}
	return e, nil
}

// Now exposes the engine's clock (a test clock in tests).
func (e *Engine) Now() time.Time { return e.cfg.Now() }

type saga struct {
	TenantID, ID, InvoiceID, CustomerID uuid.UUID
	State                               State
	Version, Attempt, StepAttempt       int
	DunningAttempt                      int
	Number                              string
	PeriodID, Currency, InvoiceStatus   string
	InvoiceVersion                      int
	TotalMinor                          int64
	FinalizedAt                         time.Time
	Snap                                Snapshot
}

func (e *Engine) load(ctx context.Context, tx pgx.Tx, tenantID, sagaID uuid.UUID) (*saga, error) {
	var s saga
	var raw []byte
	var number *string
	err := tx.QueryRow(ctx, `
		SELECT s.tenant_id, s.id, s.invoice_id, i.customer_id, s.state, s.version, s.attempt, s.step_attempt, s.dunning_attempt,
		       i.number, i.period_id, i.currency, i.status, i.version, i.total_minor, COALESCE(i.finalized_at, i.created_at), i.snapshot
		FROM invoice_sagas s JOIN invoices i ON i.tenant_id = s.tenant_id AND i.id = s.invoice_id
		WHERE s.tenant_id = $1 AND s.id = $2
		FOR UPDATE OF s`, tenantID, sagaID).Scan(&s.TenantID, &s.ID, &s.InvoiceID, &s.CustomerID, &s.State, &s.Version, &s.Attempt,
		&s.StepAttempt, &s.DunningAttempt, &number, &s.PeriodID, &s.Currency, &s.InvoiceStatus, &s.InvoiceVersion, &s.TotalMinor, &s.FinalizedAt, &raw)
	if err != nil {
		return nil, err
	}
	if number != nil {
		s.Number = *number
	}
	if err := json.Unmarshal(raw, &s.Snap); err != nil {
		return nil, fmt.Errorf("invoice %s snapshot: %w", s.InvoiceID, err)
	}
	return &s, nil
}

// ErrNotFound means the saga does not exist.
var ErrNotFound = errors.New("saga: not found")

// Outcome of Handle.
type Outcome struct {
	// Applied is false when the event was invalid or stale for the saga's state and was ignored.
	Applied bool
	From    State
	To      State
}

// Handle applies an event to a saga: it locks the row, follows the table (including the automatic continuations),
// persists the new state, the audit rows and the outbox commands, all in tx. An invalid pair is ignored (Applied=false).
func (e *Engine) Handle(ctx context.Context, tx pgx.Tx, tenantID, sagaID uuid.UUID, ev Event, cause string, commandID *uuid.UUID) (Outcome, error) {
	s, err := e.load(ctx, tx, tenantID, sagaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Outcome{}, ErrNotFound
	}
	if err != nil {
		return Outcome{}, err
	}
	return e.handleLoaded(ctx, tx, s, ev, cause, commandID)
}

func (e *Engine) guardCtx(s *saga) Ctx {
	return Ctx{TotalMinor: s.TotalMinor, Paid: s.InvoiceStatus == "paid", DunningAttempt: s.DunningAttempt, MaxDunning: MaxDunning}
}

func (e *Engine) handleLoaded(ctx context.Context, tx pgx.Tx, s *saga, ev Event, cause string, commandID *uuid.UUID) (Outcome, error) {
	out := Outcome{From: s.State}
	for {
		res, ok := Next(s.State, ev, e.guardCtx(s))
		if !ok {
			if !out.Applied {
				e.ignored.Add(ctx, 1, metric.WithAttributes(attribute.String("state", string(s.State)), attribute.String("event", string(ev))))
			}
			out.To = s.State
			return out, nil
		}
		if err := e.transition(ctx, tx, s, ev, res, cause, commandID); err != nil {
			return out, err
		}
		out.Applied, out.To = true, s.State
		switch s.State {
		case PdfRendered, Paid, Uncollectible, Refunded:
			ev, cause, commandID = EvContinue, "auto", nil
		default:
			return out, nil
		}
	}
}

func (e *Engine) transition(ctx context.Context, tx pgx.Tx, s *saga, ev Event, res Result, cause string, commandID *uuid.UUID) error {
	now := e.cfg.Now()
	from := s.State
	next := res.To

	attempt := s.Attempt
	var stepAttempt int
	dunning := s.DunningAttempt
	var deadline, nextAttemptAt *time.Time
	switch res.Effect {
	case EffRender, EffCharge, EffChargeDunning, EffQueryStatus, EffRefund, EffNotify:
		attempt++
		stepAttempt = 1
		d := now.Add(e.cfg.StepTimeout)
		deadline = &d
		if res.Effect == EffChargeDunning {
			dunning++
		}
	case EffScheduleDunning:
		delays := e.dunningDelays(s)
		at := now.Add(delays[min(s.DunningAttempt, len(delays)-1)])
		nextAttemptAt = &at
	}
	if ev == EvExhausted && next == PaymentUnknown && from == PaymentUnknown {
		// Parked for the operator: keep the attempt counters, drop the deadline.
		stepAttempt = s.StepAttempt
	}
	lastErr := ""
	if ev == EvExhausted {
		lastErr = "step timed out after " + fmt.Sprint(MaxStepAttempts) + " attempts"
	}
	tag, err := tx.Exec(ctx, `
		UPDATE invoice_sagas SET state = $1, version = version + 1, attempt = $2, step_attempt = $3, step_deadline = $4,
		       dunning_attempt = $5, next_attempt_at = $6, last_error = NULLIF($7, ''), updated_at = $8
		WHERE tenant_id = $9 AND id = $10 AND version = $11 AND state = $12`,
		string(next), attempt, stepAttempt, deadline, dunning, nextAttemptAt, lastErr, now, s.TenantID, s.ID, s.Version, string(from))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("saga %s changed concurrently", s.ID)
	}
	var seq int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) + 1 FROM saga_events WHERE tenant_id = $1 AND saga_id = $2`, s.TenantID, s.ID).Scan(&seq); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saga_events (tenant_id, saga_id, seq, from_state, to_state, cause, command_id, at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		s.TenantID, s.ID, seq, string(from), string(next), cause, commandID, now); err != nil {
		return err
	}
	e.transitions.Add(ctx, 1, metric.WithAttributes(attribute.String("from", string(from)), attribute.String("to", string(next)),
		attribute.String("cause", strings.SplitN(cause, ":", 2)[0])))

	s.State, s.Version, s.Attempt, s.StepAttempt, s.DunningAttempt = next, s.Version+1, attempt, stepAttempt, dunning

	// Invoice status mirrors the saga's coarse outcome.
	status := ""
	switch {
	case next == Paid, from == PdfRendered && next == NotifyRequested:
		status = "paid"
	case next == Uncollectible:
		status = "uncollectible"
	case next == Voided:
		status = "void"
	}
	if status != "" {
		if _, err := tx.Exec(ctx, `UPDATE invoices SET status = $1 WHERE tenant_id = $2 AND id = $3`, status, s.TenantID, s.InvoiceID); err != nil {
			return err
		}
		s.InvoiceStatus = status
	}

	if res.Effect != EffNone && res.Effect != EffScheduleDunning {
		if err := e.issue(ctx, tx, s, res.Effect); err != nil {
			return err
		}
	}
	switch next {
	case Paid:
		return e.fact(ctx, tx, s, FactPaid, func(f *eventsv1.InvoiceFact) {
			f.Kind = &eventsv1.InvoiceFact_Paid_{Paid: &eventsv1.InvoiceFact_Paid{}}
		})
	case Voided:
		return e.fact(ctx, tx, s, FactVoided, func(f *eventsv1.InvoiceFact) {
			f.Kind = &eventsv1.InvoiceFact_Voided_{Voided: &eventsv1.InvoiceFact_Voided{Reason: cause}}
		})
	}
	return nil
}

func (e *Engine) dunningDelays(s *saga) []time.Duration {
	if s.Snap.Environment == "sandbox" {
		return e.cfg.DunningSandbox
	}
	return e.cfg.DunningLive
}

// issue writes the outbox row of the command an effect asks for. Its message id is the command id, so issuing the same
// (saga, step, attempt) twice is a no-op.
func (e *Engine) issue(ctx context.Context, tx pgx.Tx, s *saga, eff Effect) error {
	step, route := "", ""
	env := &commandsv1.CommandEnvelope{}
	switch eff {
	case EffRender:
		step, route = "render", rabbitx.QueueInvoiceRender
		lines, err := e.snapshotLines(ctx, tx, s)
		if err != nil {
			return err
		}
		env.Command = &commandsv1.CommandEnvelope_RenderInvoicePdf{RenderInvoicePdf: &commandsv1.RenderInvoicePdf{Invoice: e.invoiceSnapshot(s, lines)}}
	case EffCharge, EffChargeDunning:
		step, route = "charge", rabbitx.QueuePaymentCharge
		env.Command = &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{
			InvoiceId: s.InvoiceID.String(), AmountMinor: s.TotalMinor, Currency: s.Currency, PaymentMethod: s.Snap.PaymentMethod,
			DunningAttempt: int32(s.DunningAttempt), IdempotencyKey: ChargeKey(s.InvoiceID, s.DunningAttempt), //nolint:gosec // 0..3
		}}
	case EffQueryStatus:
		step, route = "status", rabbitx.QueuePaymentStatus
		env.Command = &commandsv1.CommandEnvelope_QueryPaymentStatus{QueryPaymentStatus: &commandsv1.QueryPaymentStatus{
			InvoiceId: s.InvoiceID.String(), IdempotencyKey: ChargeKey(s.InvoiceID, s.DunningAttempt)}}
	case EffRefund:
		step, route = "refund", rabbitx.QueuePaymentRefund
		env.Command = &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{
			InvoiceId: s.InvoiceID.String(), AmountMinor: s.TotalMinor, Currency: s.Currency,
			IdempotencyKey: s.InvoiceID.String() + ":refund"}}
	case EffNotify:
		step, route = "notify", rabbitx.QueueNotificationSend
		env.Command = &commandsv1.CommandEnvelope_SendNotification{SendNotification: e.notification(s)}
	default:
		return fmt.Errorf("saga: unknown effect %q", eff)
	}
	return e.publishCommand(ctx, tx, s, env, step, route)
}

// ChargeKey is the PSP idempotency key of a charge: stable across retries of the same dunning attempt.
func ChargeKey(invoice uuid.UUID, dunning int) string {
	return fmt.Sprintf("%s:charge:%d", invoice, dunning)
}

// CommandID is uuidv5(saga_id|step|attempt).
func CommandID(sagaID uuid.UUID, step string, attempt int) uuid.UUID {
	return uuid.NewSHA1(commandNS, fmt.Appendf(nil, "%s|%s|%d", sagaID, step, attempt))
}

func (e *Engine) publishCommand(ctx context.Context, tx pgx.Tx, s *saga, env *commandsv1.CommandEnvelope, step, route string) error {
	id := CommandID(s.ID, step, s.Attempt)
	env.CommandId, env.SagaId, env.TenantId = id.String(), s.ID.String(), s.TenantID.String()
	env.Attempt = int32(s.Attempt) //nolint:gosec // small
	env.IssuedAt = timestamppb.New(e.cfg.Now())
	env.Environment = plansv1.Environment_ENVIRONMENT_LIVE
	if s.Snap.Environment == "sandbox" {
		env.Environment = plansv1.Environment_ENVIRONMENT_SANDBOX
	}
	body, err := proto.Marshal(env)
	if err != nil {
		return err
	}
	return EnqueueOutbox(ctx, tx, s.TenantID, id, rabbitx.ExchangeCommands, route, outbox.DestRabbitMQ, body)
}

// EnqueueOutbox inserts one outbox row (a no-op when the message id exists) and signals the relay.
func EnqueueOutbox(ctx context.Context, tx pgx.Tx, tenantID, messageID uuid.UUID, topic, key, dest string, payload []byte) error {
	hdr, _ := json.Marshal(map[string]string{outbox.DestinationHeader: dest, outbox.MessageIDHeader: messageID.String()})
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (tenant_id, topic, partition_key, payload, headers, message_id)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (message_id) DO NOTHING`, tenantID, topic, key, payload, hdr, messageID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT pg_notify($1, '')`, outbox.NotifyChannel)
	return err
}

// Fact versions: consumers dedup on (invoice_id, version).
const (
	FactFinalized int64 = 1
	FactPaid      int64 = 2
	FactVoided    int64 = 3
)

func (e *Engine) fact(ctx context.Context, tx pgx.Tx, s *saga, version int64, set func(*eventsv1.InvoiceFact)) error {
	f := &eventsv1.InvoiceFact{
		InvoiceId: s.InvoiceID.String(), TenantId: s.TenantID.String(), CustomerId: s.CustomerID.String(), PeriodId: s.PeriodID,
		Version: version, TotalMinor: s.TotalMinor, Currency: s.Currency, Number: s.Number, OccurredAt: timestamppb.New(e.cfg.Now()),
	}
	set(f)
	b, err := proto.Marshal(f)
	if err != nil {
		return err
	}
	return EnqueueOutbox(ctx, tx, s.TenantID, uuid.NewSHA1(factNS, fmt.Appendf(nil, "%s|%d", s.InvoiceID, version)),
		InvoicesTopic, s.TenantID.String(), outbox.DestKafka, b)
}

// FinalizedFact is the fact the run publishes when it assigns an invoice's number.
func FinalizedFact(ctx context.Context, tx pgx.Tx, now time.Time, tenantID, invoiceID, customerID uuid.UUID, period, number, currency string, total int64) error {
	f := &eventsv1.InvoiceFact{
		InvoiceId: invoiceID.String(), TenantId: tenantID.String(), CustomerId: customerID.String(), PeriodId: period,
		Version: FactFinalized, TotalMinor: total, Currency: currency, Number: number, OccurredAt: timestamppb.New(now),
		Kind: &eventsv1.InvoiceFact_Finalized_{Finalized: &eventsv1.InvoiceFact_Finalized{}},
	}
	b, err := proto.Marshal(f)
	if err != nil {
		return err
	}
	return EnqueueOutbox(ctx, tx, tenantID, uuid.NewSHA1(factNS, fmt.Appendf(nil, "%s|%d", invoiceID, FactFinalized)),
		InvoicesTopic, tenantID.String(), outbox.DestKafka, b)
}

func (e *Engine) notification(s *saga) *commandsv1.SendNotification {
	tmpl := "invoice_paid.v1"
	if s.InvoiceStatus == "uncollectible" {
		tmpl = "payment_failed.v1"
	}
	amount := quantity.FormatUnits(s.TotalMinor, s.Snap.CurrencyExponent)
	return &commandsv1.SendNotification{Template: tmpl, To: s.Snap.CustomerEmail, Data: map[string]string{
		"invoice_number": s.Number, "invoice_id": s.InvoiceID.String(), "period": s.PeriodID, "amount": amount,
		"currency": s.Currency, "customer_name": s.Snap.CustomerName, "tenant_name": s.Snap.TenantName,
		"sandbox": fmt.Sprint(s.Snap.Environment == "sandbox"),
	}}
}
