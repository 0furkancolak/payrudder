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
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/outbox"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
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
	// Provider names the payment provider of the customer's subscription; empty is the tenant's default.
	Provider         string `json:"provider,omitempty"`
	TenantName       string `json:"tenant_name"`
	Environment      string `json:"environment"` // "sandbox" | "live"
	CurrencyExponent int    `json:"currency_exponent"`
	LogoObjectKey    string `json:"logo_object_key,omitempty"`
	// ProviderMails: the provider takes the payment and mails the customer its own receipt (decided when the draft was made), so Payrudder sends none.
	ProviderMails bool `json:"provider_mails,omitempty"`
	// The postal addresses as printed lines, frozen when the draft was made.
	SellerAddress   []string `json:"seller_address,omitempty"`
	CustomerAddress []string `json:"customer_address,omitempty"`
	// E-invoicing (phase 30): the tenant's e_invoice_on and the parties, frozen with the draft. Empty when the tenant does not use it.
	EInvoiceOn string `json:"e_invoice_on,omitempty"`
	Seller     *Party `json:"seller,omitempty"`
	Buyer      *Party `json:"buyer,omitempty"`
	// Collection is "link" for an invoice the customer pays through a payment link: the saga waits for the payment instead of charging a stored method.
	Collection string `json:"collection,omitempty"`
	// SkipMail: no mail goes to the customer (a guest paying an ad-hoc link has no address on file).
	SkipMail bool `json:"skip_mail,omitempty"`
	// Description is what an ad-hoc link is for, shown on the payment page.
	Description string `json:"description,omitempty"`
	// Country of the customer (ISO 3166-1 alpha-2), for the payment methods routing picks.
	Country string `json:"country,omitempty"`
	// BankTransfer are the tenant's bank details, for an invoice whose provider is a bank transfer; frozen when the draft was made. The payment reference
	// is worked out from the template and the invoice number.
	BankTransfer *BankTransfer `json:"bank_transfer,omitempty"`
}

// CollectionLink is the Snapshot.Collection of an invoice the customer pays through a payment link.
const CollectionLink = "link"

// BankTransfer is where an invoice is paid by bank transfer.
type BankTransfer struct {
	BankName          string `json:"bank_name"`
	AccountHolder     string `json:"account_holder"`
	IBAN              string `json:"iban"`
	BIC               string `json:"bic,omitempty"`
	ReferenceTemplate string `json:"reference_template,omitempty"`
}

// Instructions are the bank details with the payment reference of an invoice, or nil when the invoice is not paid by bank transfer.
func (s Snapshot) Instructions(number string, invoiceID uuid.UUID) *BankInstructions {
	b := s.BankTransfer
	if b == nil {
		return nil
	}
	return &BankInstructions{BankName: b.BankName, AccountHolder: b.AccountHolder, IBAN: b.IBAN, BIC: b.BIC,
		Reference: banktransfer.Reference(b.ReferenceTemplate, number, invoiceID.String())}
}

// BankInstructions are the bank details and the reference the customer writes on the transfer.
type BankInstructions struct{ BankName, AccountHolder, IBAN, BIC, Reference string }

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
	// RetryHours are the tenant's own dunning delays; nil means the service-wide defaults.
	RetryHours []int32
	// CreditedMinor is the sum of the invoice's credit notes, read when a void asks what is left to refund.
	CreditedMinor int64
}

func (e *Engine) load(ctx context.Context, tx pgx.Tx, tenantID, sagaID uuid.UUID) (*saga, error) {
	q := sqlc.New(tx)
	r, err := q.LockSagaWithInvoice(ctx, sqlc.LockSagaWithInvoiceParams{TenantID: tenantID, ID: sagaID})
	if err != nil {
		return nil, err
	}
	s := saga{
		TenantID: r.TenantID, ID: r.ID, InvoiceID: r.InvoiceID, CustomerID: r.CustomerID, State: State(r.State), Version: int(r.Version), Attempt: int(r.Attempt),
		StepAttempt: int(r.StepAttempt), DunningAttempt: int(r.DunningAttempt), PeriodID: r.PeriodID, Currency: r.Currency, InvoiceStatus: r.Status,
		InvoiceVersion: int(r.InvoiceVersion), TotalMinor: r.TotalMinor, FinalizedAt: r.FinalizedAt,
	}
	if r.Number != nil {
		s.Number = *r.Number
	}
	if err := json.Unmarshal(r.Snapshot, &s.Snap); err != nil {
		return nil, fmt.Errorf("invoice %s snapshot: %w", s.InvoiceID, err)
	}
	switch settings, err := q.GetDunningSettings(ctx, tenantID); {
	case errors.Is(err, pgx.ErrNoRows):
		s.RetryHours = nil
	case err != nil:
		return nil, fmt.Errorf("dunning settings: %w", err)
	default:
		s.RetryHours = settings.RetryHours
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
	return Ctx{TotalMinor: s.TotalMinor, CreditedMinor: s.CreditedMinor, Paid: s.InvoiceStatus == "paid", DunningAttempt: s.DunningAttempt, MaxDunning: len(e.dunningDelays(s)),
		ProviderMails: s.Snap.ProviderMails || s.Snap.SkipMail, Link: s.Snap.Collection == CollectionLink}
}

func (e *Engine) handleLoaded(ctx context.Context, tx pgx.Tx, s *saga, ev Event, cause string, commandID *uuid.UUID) (Outcome, error) {
	out := Outcome{From: s.State}
	if ev == EvVoid {
		// What a void refunds is what the credit notes left: an invoice credited in full has nothing to give back.
		credited, err := sqlc.New(tx).SumCreditNotes(ctx, sqlc.SumCreditNotesParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
		if err != nil {
			return out, err
		}
		s.CreditedMinor = credited
	}
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

// step is what a transition writes to the saga row.
type step struct {
	attempt, stepAttempt, dunning int
	deadline, nextAttemptAt       *time.Time
	lastErr                       string
}

func (e *Engine) planStep(s *saga, ev Event, res Result, now time.Time) step {
	st := step{attempt: s.Attempt, dunning: s.DunningAttempt}
	switch res.Effect {
	case EffRender, EffCharge, EffChargeDunning, EffQueryStatus, EffRefund, EffNotify:
		st.attempt++
		st.stepAttempt = 1
		d := now.Add(e.cfg.StepTimeout)
		st.deadline = &d
		if res.Effect == EffChargeDunning {
			st.dunning++
		}
	case EffScheduleDunning:
		delays := e.dunningDelays(s)
		at := now.Add(delays[min(s.DunningAttempt, len(delays)-1)]) // the table only schedules while DunningLeft, so delays is not empty
		st.nextAttemptAt = &at
	case EffSchedulePoll, EffAwaitTransfer:
		// Nothing is outstanding while a payment is awaited: no step deadline, only the time of the next poll. The instructions mail is a command of its own
		// (a new attempt, so a new command id) whose reply nobody waits for.
		if res.Effect == EffAwaitTransfer {
			st.attempt++
		}
		at := now.Add(e.cfg.TransferPoll)
		st.nextAttemptAt = &at
	}
	if ev == EvExhausted {
		st.lastErr = "step timed out after " + fmt.Sprint(MaxStepAttempts) + " attempts"
		if res.To == PaymentUnknown && s.State == PaymentUnknown {
			// Parked for the operator: keep the attempt counters, drop the deadline.
			st.stepAttempt = s.StepAttempt
		}
	}
	return st
}

func (e *Engine) transition(ctx context.Context, tx pgx.Tx, s *saga, ev Event, res Result, cause string, commandID *uuid.UUID) error {
	now := e.cfg.Now()
	from, next := s.State, res.To
	causeKind := strings.SplitN(cause, ":", 2)[0]
	ctx, span := otel.Tracer("github.com/0furkancolak/payrudder/services/billing-svc/saga").Start(ctx, "saga.transition",
		trace.WithAttributes(attribute.String("saga.id", s.ID.String()), attribute.String("saga.from", string(from)),
			attribute.String("saga.to", string(next)), attribute.String("saga.cause", causeKind),
			attribute.String("payrudder.tenant_id", s.TenantID.String())))
	defer span.End()

	st := e.planStep(s, ev, res, now)
	if err := e.persistTransition(ctx, tx, s, next, st, cause, commandID, now); err != nil {
		return err
	}
	e.transitions.Add(ctx, 1, metric.WithAttributes(attribute.String("from", string(from)), attribute.String("to", string(next)), attribute.String("cause", causeKind)))
	s.State, s.Version, s.Attempt, s.StepAttempt, s.DunningAttempt = next, s.Version+1, st.attempt, st.stepAttempt, st.dunning

	if err := e.mirrorInvoiceStatus(ctx, tx, s, from, next); err != nil {
		return err
	}
	if res.Effect != EffNone && res.Effect != EffScheduleDunning && res.Effect != EffSchedulePoll {
		if err := e.issue(ctx, tx, s, res.Effect); err != nil {
			return err
		}
	}
	return e.announce(ctx, tx, s, next, cause, now)
}

func (e *Engine) persistTransition(ctx context.Context, tx pgx.Tx, s *saga, next State, st step, cause string, commandID *uuid.UUID, now time.Time) error {
	q := sqlc.New(tx)
	n, err := q.TransitionSaga(ctx, sqlc.TransitionSagaParams{
		State: string(next), Attempt: i32(st.attempt), StepAttempt: i32(st.stepAttempt), StepDeadline: st.deadline, DunningAttempt: i32(st.dunning),
		NextAttemptAt: st.nextAttemptAt, LastError: st.lastErr, UpdatedAt: now, TenantID: s.TenantID, ID: s.ID, Version: i32(s.Version), FromState: string(s.State),
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("saga %s changed concurrently", s.ID)
	}
	return appendEvent(ctx, q, s, string(s.State), string(next), cause, commandID, now)
}

func appendEvent(ctx context.Context, q *sqlc.Queries, s *saga, from, to, cause string, commandID *uuid.UUID, now time.Time) error {
	seq, err := q.NextSagaEventSeq(ctx, sqlc.NextSagaEventSeqParams{TenantID: s.TenantID, SagaID: s.ID})
	if err != nil {
		return err
	}
	return q.InsertSagaEvent(ctx, sqlc.InsertSagaEventParams{TenantID: s.TenantID, SagaID: s.ID, Seq: seq, FromState: from, ToState: to, Cause: cause, CommandID: commandID, At: now})
}

// mirrorInvoiceStatus sets the invoice's coarse status from the saga's outcome.
func (e *Engine) mirrorInvoiceStatus(ctx context.Context, tx pgx.Tx, s *saga, from, next State) error {
	status := ""
	switch {
	case next == Paid, from == PdfRendered && next == NotifyRequested:
		status = "paid"
	case next == Uncollectible:
		status = "uncollectible"
	case next == Voided:
		status = "void"
	}
	if status == "" {
		return nil
	}
	if err := sqlc.New(tx).SetInvoiceStatus(ctx, sqlc.SetInvoiceStatusParams{Status: status, TenantID: s.TenantID, ID: s.InvoiceID}); err != nil {
		return err
	}
	s.InvoiceStatus = status
	if next == Voided {
		if err := reopenCredits(ctx, tx, s); err != nil {
			return err
		}
		// An invoice that used a coupon gives its redemption back; plans-svc is told by the scheduler (the reversal is queued in this transaction).
		return sqlc.New(tx).QueuePromotionReversal(ctx, sqlc.QueuePromotionReversalParams{TenantID: s.TenantID, ID: s.InvoiceID})
	}
	return nil
}

// reopenCredits gives back the credit balances a voided invoice consumed when its draft was made, so the customer does not lose them.
func reopenCredits(ctx context.Context, tx pgx.Tx, s *saga) error {
	q := sqlc.New(tx)
	if err := q.DeleteCreditApplicationsOfInvoice(ctx, sqlc.DeleteCreditApplicationsOfInvoiceParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID}); err != nil {
		return err
	}
	return q.ClearCreditsAppliedToInvoice(ctx, sqlc.ClearCreditsAppliedToInvoiceParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
}

// announce tells the tenant's endpoints and the invoice topic about the states that are outcomes.
func (e *Engine) announce(ctx context.Context, tx pgx.Tx, s *saga, next State, cause string, now time.Time) error {
	if typ := map[State]string{Paid: EventInvoicePaid, Voided: EventInvoiceVoided, Uncollectible: EventInvoiceUncollectible}[next]; typ != "" {
		// Reaching the state again after a retry is the same occurrence: the key is the invoice.
		if err := TenantEvent(ctx, tx, s.TenantID, typ, s.InvoiceID.String(), now,
			InvoiceData(s.InvoiceID, s.CustomerID, s.Snap.CustomerExternalID, s.Number, s.PeriodID, s.Currency, s.TotalMinor, s.InvoiceStatus)); err != nil {
			return err
		}
	}
	switch next {
	case Paid:
		if err := e.invoiceEInvoice(ctx, tx, s, "paid"); err != nil {
			return err
		}
		if err := e.linksPaid(ctx, tx, s, now); err != nil {
			return err
		}
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

func i32(n int) int32 { return int32(n) } //nolint:gosec // attempt counters and versions are small

// dunningDelays are the delays before retry 1..n: the tenant's own when set (hours, or minutes in a sandbox tenant), else the defaults.
// A tenant that set none gives up on the first declined charge.
func (e *Engine) dunningDelays(s *saga) []time.Duration {
	if s.RetryHours != nil {
		unit := time.Hour
		if s.Snap.Environment == "sandbox" {
			unit = time.Minute
		}
		out := make([]time.Duration, len(s.RetryHours))
		for i, h := range s.RetryHours {
			out[i] = time.Duration(h) * unit
		}
		return out
	}
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
		// A part payment recorded by hand is not charged again: the card is asked for what is still outstanding.
		paid, err := sqlc.New(tx).SumInvoicePayments(ctx, sqlc.SumInvoicePaymentsParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
		if err != nil {
			return err
		}
		if s.TotalMinor-paid <= 0 {
			return fmt.Errorf("saga %s: invoice %s has nothing outstanding to charge", s.ID, s.InvoiceID)
		}
		env.Command = &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{
			InvoiceId: s.InvoiceID.String(), InvoiceNumber: s.Number, AmountMinor: s.TotalMinor - paid, Currency: s.Currency, PaymentMethod: s.Snap.PaymentMethod, Provider: s.Snap.Provider,
			DunningAttempt: int32(s.DunningAttempt), IdempotencyKey: ChargeKey(s.InvoiceID, s.DunningAttempt), //nolint:gosec // 0..3
		}}
	case EffQueryStatus:
		step, route = "status", rabbitx.QueuePaymentStatus
		env.Command = &commandsv1.CommandEnvelope_QueryPaymentStatus{QueryPaymentStatus: &commandsv1.QueryPaymentStatus{
			InvoiceId: s.InvoiceID.String(), IdempotencyKey: ChargeKey(s.InvoiceID, s.DunningAttempt)}}
	case EffRefund:
		step, route = "refund", rabbitx.QueuePaymentRefund
		// Credit notes already gave part of the invoice back (to the card, or as a credit balance).
		credited, err := sqlc.New(tx).SumCreditNotes(ctx, sqlc.SumCreditNotesParams{TenantID: s.TenantID, InvoiceID: s.InvoiceID})
		if err != nil {
			return err
		}
		if s.TotalMinor-credited <= 0 {
			return fmt.Errorf("saga %s: invoice %s is credited in full, there is nothing to refund", s.ID, s.InvoiceID)
		}
		env.Command = &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{
			InvoiceId: s.InvoiceID.String(), AmountMinor: s.TotalMinor - credited, Currency: s.Currency,
			IdempotencyKey: s.InvoiceID.String() + ":refund"}}
	case EffNotify:
		step, route = "notify", rabbitx.QueueNotificationSend
		env.Command = &commandsv1.CommandEnvelope_SendNotification{SendNotification: e.notification(s)}
	case EffAwaitTransfer:
		step, route = "notify", rabbitx.QueueNotificationSend
		env.Command = &commandsv1.CommandEnvelope_SendNotification{SendNotification: e.transferNotification(s)}
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
	h := map[string]string{outbox.DestinationHeader: dest, outbox.MessageIDHeader: messageID.String()}
	// The relay and every consumer continue the trace of the transition that produced the message.
	kafkax.InjectTrace(ctx, h, otel.GetTextMapPropagator())
	hdr, _ := json.Marshal(h)
	q := sqlc.New(tx)
	if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{TenantID: tenantID, Topic: topic, PartitionKey: key, Payload: payload, Headers: hdr, MessageID: messageID}); err != nil {
		return err
	}
	return q.NotifyOutbox(ctx, outbox.NotifyChannel)
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
	data := map[string]string{
		"invoice_number": s.Number, "invoice_id": s.InvoiceID.String(), "period": s.PeriodID, "amount": amount,
		"currency": s.Currency, "customer_name": s.Snap.CustomerName, "tenant_name": s.Snap.TenantName,
		"sandbox": fmt.Sprint(s.Snap.Environment == "sandbox"),
	}
	if tmpl == "payment_failed.v1" {
		instructionData(s, data) // a customer who did not pay by transfer is told once more where to
	}
	return &commandsv1.SendNotification{Template: tmpl, To: s.Snap.CustomerEmail, Data: data}
}
