// Package handler executes payment commands against a PaymentProvider and translates the outcome into replies.
// It holds no money logic: amounts come from the command.
package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/buyer"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/health"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Handler routes commands by environment (ADR 0011) and, for live tenants, by their provider account.
type Handler struct {
	// Sandbox is mock-psp, which serves every sandbox command except one the tenant has connected a provider's test mode for (see pickSandbox): a sandbox
	// command can never reach a live account of a provider, whatever the configuration says.
	Sandbox payments.PaymentProvider
	// Live is PAYMENTS_LIVE_PROVIDER, the provider for live tenants that have no account of their own. Nil replies
	// PaymentErrored{PROVIDER_NOT_CONFIGURED}.
	Live     payments.PaymentProvider
	Attempts store.Attempts
	// Resolver, when set, builds the adapter of a live tenant's own provider account (its default account for charges,
	// the provider of the original charge for refunds and status queries).
	Resolver *Resolver
	// Ledger, when its DB is set, records every charge and refund outcome.
	Ledger store.Ledger
	Log    *slog.Logger
	// Demo makes every tenant, live ones too, use the sandbox provider: a demo stack can never reach a real provider.
	Demo bool
	// AllowMockLive lets a live tenant be served by mock-psp (PAYMENTS_LIVE_PROVIDER=mockpsp): a local stack, where there is no real provider. Off, a live
	// command that would reach the mock is refused, whatever the configuration says: live never reaches mock-psp, as sandbox never reaches a real provider.
	AllowMockLive bool
	// Tenants, when set, is asked for the environment of the command's tenant, and a command that disagrees is refused (dead-lettered). Without it a command
	// is still refused when it names no environment.
	Tenants *env.Directory
	// EInvoice executes IssueEInvoice commands (phase 30); nil makes them poison.
	EInvoice workerx.Handler
	// Transfers holds the pending rows, receipts and closures of providers whose payments an operator confirms by hand (a bank transfer).
	Transfers store.Transfers
	// TransferWindow is how long such a payment waits for its money when the tenant's account does not say (PAYMENTS_TRANSFER_WINDOW_DAYS).
	TransferWindow time.Duration
	// Breakers, when set, holds a circuit breaker per provider account: a new charge to an account whose circuit is open is not sent, and the command is retried later
	// (docs/provider-health.md).
	Breakers *health.Breakers
	// Failovers, when set, says where a new charge may go instead when the account it was routed to definitely did not process it. Nil never fails over.
	Failovers Failovers
	// Customers, when set, is asked for the customer of a charge whose provider vets the payer (Capabilities.NeedsBuyer: iyzico), so the charge carries their own name,
	// phone, tax id and address. Nil sends no buyer, and such a provider falls back to the account's explicit defaults or refuses with the fields named.
	Customers buyer.Reader
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Failovers is the configuration of failover: the account a charge to provider may go to instead, if one is set for the tenant and environment.
type Failovers interface {
	Failover(ctx context.Context, tenant uuid.UUID, environment, provider string) (string, bool, error)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Err codes carried by PaymentErrored.
const (
	CodeNotConfigured = "PROVIDER_NOT_CONFIGURED"
	CodeTimeout       = "TIMEOUT"
	CodeRejected      = "REJECTED"
	CodeCurrency      = "CURRENCY_NOT_SUPPORTED"
	// CodePending: the provider accepted the charge and settles it later; billing asks for the status.
	CodePending = "PENDING"
	// CodeRefundUnknown: this refund was started before and nothing records how it went. It is not sent again, because a provider that does not dedupe by key
	// would refund twice. The credit note then shows the refund as failed with this code, and an operator checks the refund at the provider.
	CodeRefundUnknown = "REFUND_OUTCOME_UNKNOWN"
)

// BuyerIncomplete starts the decline code of a charge the provider could not take because the customer record lacks what it needs: "buyer_incomplete:phone,tax_id",
// the fields of the record after the colon. The provider was not asked, nothing was charged, and the charge is declined (so the retries of dunning run, each one reading
// the customer again, which the seller has completed by then) rather than answered with an error, which would leave the invoice waiting for a status that never comes.
// Default (not agreed): the contract (docs/payment-provider-contract.md) calls a refusal of the request a typed error, not a decline; this one is the customer's, not the card's.
const BuyerIncomplete = "buyer_incomplete"

// providerFor picks the provider of a command. A live tenant with its own provider account is served by it; the others by the
// configured live provider. Refunds and status queries follow the provider the invoice was charged through.
//
// Both directions are guarded here, in one place: a sandbox command is served by h.Sandbox or by the tenant's own sandbox account at a provider, which must be a
// test-mode one (a live endpoint or a live key is refused), and a live command is never served by the mock unless the stack says it has no real provider
// (AllowMockLive, or Demo, which serves everyone from the mock). method is the payment method of a charge, empty for the others.
func (h *Handler) providerFor(ctx context.Context, tenant uuid.UUID, environment env.Env, invoice uuid.UUID, charge bool, named, method string) (payments.PaymentProvider, error) {
	p, err := h.pickProvider(ctx, tenant, environment, invoice, charge, named, method)
	if err != nil || p == nil {
		return p, err
	}
	if err := h.guard(environment, p); err != nil {
		return nil, err
	}
	return p, nil
}

// guard refuses a provider that does not belong to the environment of the command.
func (h *Handler) guard(environment env.Env, p payments.PaymentProvider) error {
	switch {
	case h.Demo:
	case environment.IsSandbox() && p != h.Sandbox:
		// Only an account adapter (the resolver builds them) can be the tenant's own, and it must not be a live one.
		acct, ok := p.(payments.Provider)
		if !ok || IsMock(p) {
			return fmt.Errorf("%w: a sandbox command was routed to %q", ErrWrongProvider, p.Name())
		}
		if err := payments.CheckLivemode(acct, env.Sandbox.String()); err != nil {
			return fmt.Errorf("%w: a sandbox command was routed to %q: %w", ErrWrongProvider, p.Name(), err)
		}
	case environment.IsLive() && IsMock(p) && !h.AllowMockLive:
		return fmt.Errorf("%w: a live command was routed to the mock provider %q", ErrWrongProvider, p.Name())
	}
	return nil
}

// ErrWrongProvider is a routing that crosses the environments; the command is answered PROVIDER_NOT_CONFIGURED, never executed.
var ErrWrongProvider = errors.New("handler: provider does not belong to the command's environment")

// IsMock reports whether p is mock-psp (the sandbox provider).
func IsMock(p payments.PaymentProvider) bool {
	_, ok := p.(*payments.MockPSP)
	return ok
}

// ScenarioPrefix starts the payment methods of the sandbox scenarios (pm_sandbox_success, pm_sandbox_declined...): mock-psp plays them, a provider never sees one.
const ScenarioPrefix = "pm_sandbox_"

// pickSandbox picks the provider of a command of a sandbox tenant: mock-psp, unless the tenant connected a provider's test mode (an account stored for the
// sandbox, never a live one: the lookup names the sandbox environment, and the resolver refuses a live endpoint or key). A charge goes to that account when the
// payment method is the provider's own token, not one of the scenarios, and the subscription's provider or else the tenant's default account has one; a refund
// or a status query follows the provider the invoice was charged through. Anything else is mock-psp, so the scenarios keep working beside a connection.
func (h *Handler) pickSandbox(ctx context.Context, tenant uuid.UUID, invoice uuid.UUID, charge bool, named, method string) (payments.PaymentProvider, error) {
	sandbox := env.Sandbox.String()
	if h.Resolver == nil {
		return h.Sandbox, nil
	}
	if charge {
		if method == "" || strings.HasPrefix(method, ScenarioPrefix) {
			return h.Sandbox, nil
		}
		if named != "" && named != h.Sandbox.Name() {
			p, err := h.Resolver.Named(ctx, tenant, named, sandbox)
			if errors.Is(err, store.ErrNoAccount) {
				return h.Sandbox, nil
			}
			return p, err
		}
		p, ok, err := h.Resolver.Default(ctx, tenant, sandbox)
		if err != nil || !ok {
			return h.Sandbox, err
		}
		return p, nil
	}
	name, ok, err := h.Attempts.ChargeProvider(ctx, tenant, invoice)
	if err != nil {
		return nil, err
	}
	if !ok || name == h.Sandbox.Name() {
		return h.Sandbox, nil
	}
	return h.Resolver.Named(ctx, tenant, name, sandbox)
}

func (h *Handler) pickProvider(ctx context.Context, tenant uuid.UUID, environment env.Env, invoice uuid.UUID, charge bool, named, method string) (payments.PaymentProvider, error) {
	if h.Demo {
		return h.Sandbox, nil
	}
	if environment.IsSandbox() {
		return h.pickSandbox(ctx, tenant, invoice, charge, named, method)
	}
	if h.Resolver != nil {
		if charge && named != "" {
			// The subscription names its provider (it was routed there): that account, not the tenant's default.
			if h.Live != nil && h.Live.Name() == named {
				return h.Live, nil
			}
			return h.Resolver.Named(ctx, tenant, named, env.Live.String())
		}
		if charge {
			p, ok, err := h.Resolver.Default(ctx, tenant, env.Live.String())
			if err != nil {
				return nil, err
			}
			if ok {
				return p, nil
			}
		} else if name, ok, err := h.Attempts.ChargeProvider(ctx, tenant, invoice); err != nil {
			return nil, err
		} else if ok && (h.Live == nil || h.Live.Name() != name) {
			return h.Resolver.Named(ctx, tenant, name, env.Live.String())
		} else if !ok {
			// Never charged through us (an invoice settled elsewhere): the tenant's default account is the best guess.
			p, found, err := h.Resolver.Default(ctx, tenant, env.Live.String())
			if err != nil {
				return nil, err
			}
			if found {
				return p, nil
			}
		}
	}
	return h.Live, nil
}

// capabilities of a provider; a provider that does not declare any is a plain, idempotent one (what mock-psp is).
func capabilities(p payments.PaymentProvider) payments.Capabilities {
	if cp, ok := p.(payments.Provider); ok {
		return cp.Capabilities()
	}
	return payments.Capabilities{Name: p.Name(), Refunds: true, IdempotentCharges: true, IdempotentRefunds: true}
}

// ledgerRetries are the pauses before the further attempts to write a ledger row.
var ledgerRetries = []time.Duration{50 * time.Millisecond, 250 * time.Millisecond}

// record writes a ledger row, trying again after a short pause (Append writes a fact once, so a repeat is harmless). A failure that remains is logged, counted
// and never fails the command: the provider has already been called, and calling it again for the sake of a bookkeeping error could charge or refund twice. The
// reply still carries the outcome and is stored in payment_attempts, which guardRefund reads; the webhook or the reconciliation writes the row later.
func (h *Handler) record(ctx context.Context, e store.LedgerEntry) {
	if h.Ledger.DB == nil {
		return
	}
	_, err := h.Ledger.Append(ctx, e)
	for _, pause := range ledgerRetries {
		if err == nil || ctx.Err() != nil {
			break
		}
		time.Sleep(pause)
		_, err = h.Ledger.Append(ctx, e)
	}
	if err == nil {
		return
	}
	ledgerFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", e.Provider), attribute.String("kind", e.Kind)))
	if h.Log != nil {
		h.Log.ErrorContext(ctx, "ledger write failed", "provider", e.Provider, "kind", e.Kind, "status", e.Status, "ref", e.ProviderRef, "err", err)
	}
}

func unavailable(err error) *commandsv1.ReplyEnvelope {
	return errored(CodeNotConfigured, err.Error())
}

// codeKeys are the error keys (docs/error-keys.md) of the codes PaymentErrored carries.
var codeKeys = map[string]string{
	CodeNotConfigured: errkey.Key("payments.charge.provider_not_configured"),
	CodeTimeout:       errkey.Key("payments.charge.timeout"),
	CodeRejected:      errkey.Key("payments.charge.rejected"),
	CodeCurrency:      errkey.Key("payments.charge.currency_not_supported"),
	CodePending:       errkey.Key("payments.charge.pending"),
	// An unknown refund outcome is a timeout of sorts. No client shows a refund's error key (the credit note carries the code and the message), so it has no key of its own.
	CodeRefundUnknown: errkey.Key("payments.charge.timeout"),
}

func errored(code, msg string) *commandsv1.ReplyEnvelope {
	key, ok := codeKeys[code]
	if !ok {
		key = errkey.Default(codes.Internal)
	}
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentErrored{PaymentErrored: &commandsv1.PaymentErrored{Code: code, Message: msg, ErrorKey: key}}}
}

// Handle implements workerx.Handler.
func (h *Handler) Handle(ctx context.Context, cmd *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
	reply, err := h.handle(ctx, cmd)
	h.count(ctx, reply, err)
	return reply, err
}

// count records the outcome of a command: succeeded, declined, errored, status/refunded replies, or retry.
func (h *Handler) count(ctx context.Context, reply *commandsv1.ReplyEnvelope, err error) {
	result := "retry"
	switch {
	case err != nil && errors.Is(err, workerx.ErrPoison):
		result = "poison"
	case err != nil:
	default:
		switch reply.GetReply().(type) {
		case *commandsv1.ReplyEnvelope_PaymentSucceeded:
			result = "succeeded"
		case *commandsv1.ReplyEnvelope_PaymentDeclined:
			result = "declined"
		case *commandsv1.ReplyEnvelope_PaymentActionRequired:
			result = "action_required"
		case *commandsv1.ReplyEnvelope_PaymentErrored:
			result = "errored"
		case *commandsv1.ReplyEnvelope_PaymentRefunded:
			result = "refunded"
		case *commandsv1.ReplyEnvelope_PaymentStatus:
			result = "status"
		case *commandsv1.ReplyEnvelope_EInvoiceResult:
			result = "einvoice"
		default:
			result = "other"
		}
	}
	paymentsTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
}

var paymentsTotal = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/payrudder/services/payments-worker").Int64Counter("payments",
		metric.WithDescription("Payment commands handled, by result"))
	if err != nil {
		panic(err)
	}
	return c
}()

// ledgerFailures counts the ledger rows that could not be written after the provider was called: the money moved and the books do not show it yet.
var ledgerFailures = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/payrudder/services/payments-worker").Int64Counter("ledger_write_failures",
		metric.WithDescription("Ledger rows not written after the provider was called, by provider and kind"))
	if err != nil {
		panic(err)
	}
	return c
}()

// refundUnknown counts the refund commands answered REFUND_OUTCOME_UNKNOWN: the provider was asked before and nothing records how it went, so the refund is parked for an
// operator to check at the provider (docs/plans/22 G1, G6). A resend of the same refund counts again.
var refundUnknown = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/payrudder/services/payments-worker").Int64Counter("refund_outcome_unknown",
		metric.WithDescription("Refunds answered REFUND_OUTCOME_UNKNOWN (parked for an operator), by provider"))
	if err != nil {
		panic(err)
	}
	return c
}()

func (h *Handler) handle(ctx context.Context, cmd *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
	if cmd.GetIssueEInvoice() != nil && h.EInvoice != nil {
		return h.EInvoice.Handle(ctx, cmd)
	}
	commandID, err := uuid.Parse(cmd.GetCommandId())
	if err != nil {
		return nil, fmt.Errorf("%w: command_id", workerx.ErrPoison)
	}
	tenantID, err := uuid.Parse(cmd.GetTenantId())
	if err != nil {
		return nil, fmt.Errorf("%w: tenant_id", workerx.ErrPoison)
	}
	// A command that names no environment, or one other than its tenant's, is dead-lettered, never executed and never defaulted to live.
	environment, err := env.FromProto(cmd.GetEnvironment())
	if err == nil && h.Tenants != nil {
		err = h.Tenants.Check(ctx, tenantID, cmd.GetEnvironment(), "payment command")
	}
	if err != nil {
		return nil, workerx.RefuseEnvironment(err)
	}
	run := commandRun{id: commandID, tenant: tenantID, environment: environment, env: environment.String()}

	switch c := cmd.GetCommand().(type) {
	case *commandsv1.CommandEnvelope_ChargePayment:
		return h.charge(ctx, run, c.ChargePayment)
	case *commandsv1.CommandEnvelope_RefundPayment:
		return h.refund(ctx, run, c.RefundPayment)
	case *commandsv1.CommandEnvelope_QueryPaymentStatus:
		return h.queryStatus(ctx, run, c.QueryPaymentStatus)
	}
	return nil, fmt.Errorf("%w: not a payment command", workerx.ErrPoison)
}

type commandRun struct {
	id, tenant  uuid.UUID
	environment env.Env
	env         string
}

// providerForCommand resolves the provider of a command; a non-nil reply is the answer when there is none. A tenant whose data is being moved (ADR 0018) is
// not an answer: the command is retried (the error), never told that no provider is configured.
func (h *Handler) providerForCommand(ctx context.Context, run commandRun, invoice uuid.UUID, charge bool, named, method string) (payments.PaymentProvider, *commandsv1.ReplyEnvelope, error) {
	prov, err := h.providerFor(ctx, run.tenant, run.environment, invoice, charge, named, method)
	if errors.Is(err, tenantdir.ErrNotServing) {
		return nil, nil, err
	}
	if err != nil {
		return nil, unavailable(err), nil
	}
	if prov == nil {
		return nil, errored(CodeNotConfigured, "no live payment provider is configured"), nil
	}
	return prov, nil, nil
}

func succeededReply(chargeID string) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentSucceeded{PaymentSucceeded: &commandsv1.PaymentSucceeded{PspChargeId: chargeID}}}
}

func actionRequiredReply(ref string) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentActionRequired{PaymentActionRequired: &commandsv1.PaymentActionRequired{PspChargeId: ref}}}
}

func declinedReply(code string, final bool) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentDeclined{PaymentDeclined: &commandsv1.PaymentDeclined{DeclineCode: code, Final: final}}}
}

func (h *Handler) charge(ctx context.Context, run commandRun, ch *commandsv1.ChargePayment) (*commandsv1.ReplyEnvelope, error) {
	invoiceID, err := uuid.Parse(ch.GetInvoiceId())
	if err != nil || ch.GetIdempotencyKey() == "" {
		return nil, fmt.Errorf("%w: charge needs invoice_id and idempotency_key", workerx.ErrPoison)
	}
	prov, reply, err := h.providerForCommand(ctx, run, invoiceID, true, ch.GetProvider(), ch.GetPaymentMethod())
	if err != nil {
		return nil, err
	}
	if reply != nil {
		return reply, nil
	}
	caps := capabilities(prov)
	if !caps.CanCharge(ch.GetCurrency()) {
		return errored(CodeCurrency, fmt.Sprintf("%s cannot charge in %s", prov.Name(), ch.GetCurrency())), nil
	}
	ref, hasRef := h.account(prov)
	name := accountName(prov, ref, hasRef)
	redelivered, err := h.Attempts.Start(ctx, run.id, run.tenant, invoiceID, store.KindCharge, run.env, name, ch.GetIdempotencyKey(), ch)
	if err != nil {
		return nil, err
	}
	if redelivered && h.Failovers != nil {
		// A command that failed over goes on at the account it failed over to when it is delivered again, never back to the first: that one may be up again by now.
		if fo, ok, err := h.Attempts.FailoverAttempt(ctx, run.tenant, invoiceID, payments.FailoverPrefix(ch.GetIdempotencyKey())); err != nil {
			return nil, err
		} else if ok {
			return h.resumeFailover(ctx, run, ch, invoiceID, fo)
		}
	}
	entry := store.LedgerEntry{TenantID: run.tenant, InvoiceID: invoiceID, Provider: name, Environment: run.env, Kind: store.KindCharge,
		AmountMinor: ch.GetAmountMinor(), Currency: ch.GetCurrency(), IdempotencyKey: ch.GetIdempotencyKey(), Source: store.SourceCommand}
	req, err := h.chargeRequest(ctx, run, ch, ch.GetIdempotencyKey(), prov)
	if err != nil {
		return nil, err
	}
	_, manual := prov.(payments.ManualCollector)
	done, err := h.admit(ref, hasRef && !manual)
	if err != nil {
		// The provider was not asked. Another account may take a charge that is new (this delivery is the first), never one that may already have been sent.
		if !redelivered {
			if reply, ferr, did := h.failOver(ctx, run, ch, invoiceID, ref, hasRef, err); did {
				return reply, ferr
			}
		}
		return nil, err
	}
	if redelivered && !caps.IdempotentCharges && !manual {
		if reply, err := h.askBeforeCharging(ctx, prov, entry); reply != nil || err != nil {
			done(err)
			return reply, err
		}
	}
	res, err := prov.Charge(ctx, req)
	done(err)
	h.keepRef(ctx, run, run.id, prov, res, err)
	var pending *payments.PendingError
	if manual && errors.As(err, &pending) {
		// The customer has been told how to pay. The row marks when the payment window opened; its reference is the charge's key, so a dunning retry
		// (another key) opens a window of its own.
		entry.Status, entry.ProviderRef, entry.Detail, entry.OccurredAt = store.StatusPending, entry.IdempotencyKey, pending.Ref, h.now()
		h.record(ctx, entry)
	}
	// The one thing that lets a charge go to another account: the provider definitely did not process it, and this is its first delivery. A timeout is not that, and
	// follows the path it always did (the saga asks the provider by the key before anything is sent again).
	if err != nil && !redelivered && !manual && payments.NotProcessed(err) {
		if reply, ferr, did := h.failOver(ctx, run, ch, invoiceID, ref, hasRef, err); did {
			return reply, ferr
		}
	}
	return h.chargeOutcome(ctx, entry, res, err)
}

// account is the provider account an adapter was built for, when the resolver built it: the mock and the configured live provider are no one's account.
func (h *Handler) account(p payments.PaymentProvider) (health.Ref, bool) {
	if h.Resolver == nil {
		return health.Ref{}, false
	}
	return h.Resolver.Account(p)
}

// accountName is what a charge is recorded under: the account's key (stripe~eu), so that its status and refunds find the same account again, else the provider's name.
func accountName(p payments.PaymentProvider, ref health.Ref, ok bool) string {
	if ok {
		return ref.Provider
	}
	return p.Name()
}

// admit asks the account's circuit breaker whether the provider may be called. done records how the call went; it does nothing for a charge that has no breaker.
func (h *Handler) admit(ref health.Ref, guarded bool) (func(error), error) {
	if h.Breakers == nil || !guarded {
		return func(error) {}, nil
	}
	return h.Breakers.Allow(ref)
}

// failOver sends a charge that the account it was routed to definitely did not process to the account configured for that, as a new attempt under a new key
// (payments.FailoverKey). It reports did = false when nothing is configured or the target cannot take the charge, and the caller goes on as before.
func (h *Handler) failOver(ctx context.Context, run commandRun, ch *commandsv1.ChargePayment, invoice uuid.UUID, from health.Ref, hasFrom bool, cause error) (*commandsv1.ReplyEnvelope, error, bool) {
	if h.Failovers == nil || h.Resolver == nil || !hasFrom {
		return nil, nil, false
	}
	to, ok, err := h.Failovers.Failover(ctx, run.tenant, run.env, from.Provider)
	if err != nil || !ok || to == from.Provider {
		return nil, nil, false
	}
	p2, err := h.Resolver.Named(ctx, run.tenant, to, run.env)
	if err == nil {
		err = h.guard(run.environment, p2)
	}
	_, manual := p2.(payments.ManualCollector)
	if err != nil || manual || !capabilities(p2).CanCharge(ch.GetCurrency()) {
		return nil, nil, false
	}
	if h.Log != nil {
		h.Log.WarnContext(ctx, "charge failed over to another account", "tenant", run.tenant, "invoice", invoice, "from", from.Provider, "to", to, "cause", cause)
	}
	reply, err := h.chargeAt(ctx, run, ch, invoice, p2, payments.FailoverKey(ch.GetIdempotencyKey(), to), false)
	return reply, err, true
}

// resumeFailover goes on with a charge that failed over, at the account it failed over to, under the key it used there.
func (h *Handler) resumeFailover(ctx context.Context, run commandRun, ch *commandsv1.ChargePayment, invoice uuid.UUID, fo store.FailoverAttempt) (*commandsv1.ReplyEnvelope, error) {
	prov, reply, err := h.providerForCommand(ctx, run, invoice, true, fo.Provider, ch.GetPaymentMethod())
	if err != nil || reply != nil {
		return reply, err
	}
	return h.chargeAt(ctx, run, ch, invoice, prov, fo.Key, true)
}

// chargeAt makes the second attempt of a charge that failed over: its own attempt row (the same row on every delivery), the failover key, and the same asking before
// charging as any attempt at a provider that does not dedupe by key. It never fails over again.
func (h *Handler) chargeAt(ctx context.Context, run commandRun, ch *commandsv1.ChargePayment, invoice uuid.UUID, prov payments.PaymentProvider, key string, resumed bool) (*commandsv1.ReplyEnvelope, error) {
	ref, hasRef := h.account(prov)
	name := accountName(prov, ref, hasRef)
	attempt := uuid.NewSHA1(run.id, []byte("failover:"+key))
	redelivered, err := h.Attempts.Start(ctx, attempt, run.tenant, invoice, store.KindCharge, run.env, name, key, ch)
	if err != nil {
		return nil, err
	}
	entry := store.LedgerEntry{TenantID: run.tenant, InvoiceID: invoice, Provider: name, Environment: run.env, Kind: store.KindCharge,
		AmountMinor: ch.GetAmountMinor(), Currency: ch.GetCurrency(), IdempotencyKey: key, Source: store.SourceCommand}
	req, err := h.chargeRequest(ctx, run, ch, key, prov)
	if err != nil {
		return nil, err
	}
	done, err := h.admit(ref, hasRef)
	if err != nil {
		return nil, err // this account is down too: the command is retried, and goes on here
	}
	if (redelivered || resumed) && !capabilities(prov).IdempotentCharges {
		if reply, err := h.askBeforeCharging(ctx, prov, entry); reply != nil || err != nil {
			done(err)
			return h.keepAttempt(ctx, run, attempt, reply, err)
		}
	}
	res, err := prov.Charge(ctx, req)
	done(err)
	h.keepRef(ctx, run, attempt, prov, res, err)
	reply, err := h.chargeOutcome(ctx, entry, res, err)
	return h.keepAttempt(ctx, run, attempt, reply, err)
}

// chargeRequest is what the provider is sent for a charge: the command, and for a provider that vets the payer the customer's own details (Capabilities.NeedsBuyer). A
// customer record that cannot be read is an error and the command is retried: refusing the payment for it would blame the card.
func (h *Handler) chargeRequest(ctx context.Context, run commandRun, ch *commandsv1.ChargePayment, key string, prov payments.PaymentProvider) (payments.ChargeRequest, error) {
	r := payments.ChargeRequest{IdempotencyKey: key, InvoiceID: ch.GetInvoiceId(), AmountMinor: ch.GetAmountMinor(), Currency: ch.GetCurrency(),
		PaymentMethod: ch.GetPaymentMethod(), InvoiceNumber: ch.GetInvoiceNumber()}
	if capabilities(prov).NeedsBuyer {
		b, err := buyer.Of(ctx, h.Customers, run.tenant, ch.GetCustomerExternalId(), "")
		if err != nil {
			return r, fmt.Errorf("read the customer of the charge: %w", err)
		}
		r.Buyer = b
	}
	return r, nil
}

// keepAttempt stores the answer on the second attempt's own row (best effort: the command's row holds the answer billing gets).
func (h *Handler) keepAttempt(ctx context.Context, run commandRun, attempt uuid.UUID, reply *commandsv1.ReplyEnvelope, err error) (*commandsv1.ReplyEnvelope, error) {
	if reply != nil && err == nil {
		_ = h.Attempts.StoreFor(ctx, run.tenant, attempt, reply)
	}
	return reply, err
}

// askBeforeCharging handles the redelivery of a charge to a provider that does not dedupe by key: the provider may have been
// called before the crash, so it is asked, never charged blindly. A nil reply and error mean the charge goes ahead.
func (h *Handler) askBeforeCharging(ctx context.Context, prov payments.PaymentProvider, entry store.LedgerEntry) (*commandsv1.ReplyEnvelope, error) {
	st, err := prov.Status(ctx, entry.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	switch st.State {
	case payments.StateSucceeded:
		entry.Status, entry.ProviderRef = store.StatusSucceeded, st.ChargeID
		h.record(ctx, entry)
		return succeededReply(st.ChargeID), nil
	case payments.StateDeclined:
		entry.Status, entry.Detail = store.StatusDeclined, st.DeclineCode
		h.record(ctx, entry)
		return declinedReply(st.DeclineCode, st.Final), nil
	}
	return nil, nil
}

// keepRef stores the provider's own reference of a charge with the attempt, from the first response that carries one, whatever it says (a success, a decline, a payment that
// is processing or one that waits for the customer): a later status query asks the provider by it instead of searching (payments.RefStatuser). Only a provider that can be
// asked that way has its reference kept. Best effort: the search is the fallback, so a failure is logged and the charge goes on.
func (h *Handler) keepRef(ctx context.Context, run commandRun, attempt uuid.UUID, prov payments.PaymentProvider, res payments.ChargeResult, err error) {
	if _, ok := prov.(payments.RefStatuser); !ok {
		return
	}
	ref := res.ChargeID
	var declined *payments.DeclinedError
	var pending *payments.PendingError
	var action *payments.ActionRequiredError
	switch {
	case errors.As(err, &declined):
		ref = declined.Ref
	case errors.As(err, &pending):
		ref = pending.Ref
	case errors.As(err, &action):
		ref = action.Ref
	}
	if ref == "" {
		return
	}
	if serr := h.Attempts.SetRef(ctx, run.tenant, attempt, ref); serr != nil && h.Log != nil {
		h.Log.WarnContext(ctx, "the provider's reference of a charge was not kept", "tenant", run.tenant, "ref", ref, "err", serr)
	}
}

func (h *Handler) chargeOutcome(ctx context.Context, entry store.LedgerEntry, res payments.ChargeResult, err error) (*commandsv1.ReplyEnvelope, error) {
	var declined *payments.DeclinedError
	var pending *payments.PendingError
	var action *payments.ActionRequiredError
	var incomplete *payments.BuyerIncompleteError
	switch {
	case errors.As(err, &action):
		// Nothing was charged and nothing is a decline: the customer has to come back. No ledger row: a pending one would make the hosted page say "processing" and refuse the
		// payment the customer is being asked to make.
		return actionRequiredReply(action.Ref), nil
	case errors.As(err, &pending):
		// Accepted, not settled: the provider's notification decides. Billing queries the status, which reads the ledger.
		return errored(CodePending, pending.Error()), nil
	case err == nil:
		entry.Status, entry.ProviderRef = store.StatusSucceeded, res.ChargeID
		h.record(ctx, entry)
		return succeededReply(res.ChargeID), nil
	case errors.As(err, &declined):
		entry.Status, entry.Detail = store.StatusDeclined, declined.Code
		h.record(ctx, entry)
		return declinedReply(declined.Code, declined.Final), nil
	case errors.As(err, &incomplete):
		code := BuyerIncomplete + ":" + strings.Join(incomplete.Missing, ",")
		entry.Status, entry.Detail = store.StatusDeclined, code
		h.record(ctx, entry)
		return declinedReply(code, false), nil
	default:
		return providerFailure(err)
	}
}

// providerFailure answers a provider call that failed. A transient failure is returned to be retried through the delay queues (a provider that does not
// dedupe is asked first); a timeout is never retried blindly, billing queries the status first; anything else is a refusal.
func providerFailure(err error) (*commandsv1.ReplyEnvelope, error) {
	var transient *payments.TransientError
	switch {
	case errors.As(err, &transient):
		return nil, err
	case errors.Is(err, payments.ErrTimeout):
		return errored(CodeTimeout, "the provider did not answer in time"), nil
	default:
		return errored(CodeRejected, err.Error()), nil
	}
}

func (h *Handler) refund(ctx context.Context, run commandRun, rf *commandsv1.RefundPayment) (*commandsv1.ReplyEnvelope, error) {
	invoiceID, err := uuid.Parse(rf.GetInvoiceId())
	if err != nil || rf.GetIdempotencyKey() == "" {
		return nil, fmt.Errorf("%w: refund needs invoice_id and idempotency_key", workerx.ErrPoison)
	}
	prov, reply, err := h.providerForCommand(ctx, run, invoiceID, false, "", "")
	if err != nil {
		return nil, err
	}
	if reply != nil {
		return reply, nil
	}
	if id := rf.GetConfirmedRefundId(); id != "" {
		return h.recordConfirmedRefund(ctx, run, prov, invoiceID, rf, id)
	}
	caps := capabilities(prov)
	if !caps.Refunds {
		// The credit note then shows the refund as failed, with this reason: the money goes back outside Payrudder (for a bank transfer, by a transfer back).
		return errored(CodeRejected, prov.Name()+" has no refund API; refund the customer outside Payrudder, then record it on the credit note (Resolve refund)"), nil
	}
	redelivered, err := h.Attempts.Start(ctx, run.id, run.tenant, invoiceID, store.KindRefund, run.env, prov.Name(), rf.GetIdempotencyKey(), rf)
	if err != nil {
		return nil, err
	}
	if !caps.IdempotentRefunds {
		if reply, err := h.guardRefund(ctx, run, prov, invoiceID, rf, redelivered); reply != nil || err != nil {
			return reply, err
		}
	}
	res, err := prov.Refund(ctx, h.refundRequest(ctx, run, invoiceID, rf))
	if err != nil {
		return providerFailure(err)
	}
	h.record(ctx, refundEntry(run, prov, invoiceID, rf, res.RefundID))
	return refundedReply(res.RefundID), nil
}

// recordConfirmedRefund answers a refund an operator confirmed was made (billing's ResolveCreditNoteRefund), at the provider or outside Payrudder (which is how a provider without
// a refund API, or a bank transfer, is refunded). The provider is not asked. The fact goes into the ledger under the key the refund was asked under, so that guardRefund answers any
// later resend from it; unlike after a provider call, a ledger write that fails fails the command, which the queue delivers again: nothing has moved that a resend could repeat.
func (h *Handler) recordConfirmedRefund(ctx context.Context, run commandRun, prov payments.PaymentProvider, invoice uuid.UUID, rf *commandsv1.RefundPayment, refundID string) (*commandsv1.ReplyEnvelope, error) {
	if h.Ledger.DB != nil {
		e := refundEntry(run, prov, invoice, rf, refundID)
		e.Source, e.Detail = store.SourceManual, "confirmed by an operator"
		if _, err := h.Ledger.Append(ctx, e); err != nil {
			return nil, err
		}
	}
	return refundedReply(refundID), nil
}

func refundEntry(run commandRun, prov payments.PaymentProvider, invoice uuid.UUID, rf *commandsv1.RefundPayment, ref string) store.LedgerEntry {
	return store.LedgerEntry{TenantID: run.tenant, InvoiceID: invoice, Provider: prov.Name(), Environment: run.env, Kind: store.KindRefund, Status: store.StatusSucceeded,
		AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency(), ProviderRef: ref, IdempotencyKey: rf.GetIdempotencyKey(), Source: store.SourceCommand}
}

func refundedReply(refundID string) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentRefunded{PaymentRefunded: &commandsv1.PaymentRefunded{PspRefundId: refundID}}}
}

// guardRefund handles a refund to a provider that does not dedupe by key (Capabilities.IdempotentRefunds). The provider may already have been asked under this key:
// billing resends a refund with a new command id and the same key, and a command that failed comes back with its own id. It is never asked blindly again. The ledger
// and the earlier attempts decide: a refund known to have been made is answered with its result, one the provider refused may be tried again, and one whose outcome
// is unknown is parked for the operator (CodeRefundUnknown). There is no lookup at the provider: neither compatibility matrix documents one. A nil reply and error
// mean the refund goes ahead.
func (h *Handler) guardRefund(ctx context.Context, run commandRun, prov payments.PaymentProvider, invoice uuid.UUID, rf *commandsv1.RefundPayment, redelivered bool) (*commandsv1.ReplyEnvelope, error) {
	key := rf.GetIdempotencyKey()
	if h.Ledger.DB != nil {
		if ref, ok, err := h.Ledger.RefundFact(ctx, run.tenant, invoice, key); err != nil {
			return nil, err
		} else if ok {
			return refundedReply(ref), nil
		}
	}
	earlier, err := h.Attempts.RefundsUnder(ctx, run.tenant, invoice, key, run.id)
	if err != nil {
		return nil, err
	}
	unknown := redelivered // this command's own earlier delivery asked the provider, or was about to
	for _, r := range earlier {
		switch {
		case r.GetPaymentRefunded() != nil:
			// The ledger row may be what is missing (its write failed): write it now.
			ref := r.GetPaymentRefunded().GetPspRefundId()
			h.record(ctx, refundEntry(run, prov, invoice, rf, ref))
			return refundedReply(ref), nil
		case r.GetPaymentErrored().GetCode() == CodeRejected: // the provider said no: nothing moved
		default: // no answer (STARTED), a timeout, or an answer that cannot be read
			unknown = true
		}
	}
	if unknown {
		refundUnknown.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", prov.Name())))
		return errored(CodeRefundUnknown, fmt.Sprintf("%s was asked for this refund before and nothing records how it went, so it is not asked again: %s does not dedupe by key and could refund twice. "+
			"Check the refund at %s, then resolve it on the credit note (Resolve refund): record it as made with the provider's reference, or ask for it again", prov.Name(), prov.Name(), prov.Name())), nil
	}
	return nil, nil
}

func (h *Handler) queryStatus(ctx context.Context, run commandRun, q *commandsv1.QueryPaymentStatus) (*commandsv1.ReplyEnvelope, error) {
	invoiceID, err := uuid.Parse(q.GetInvoiceId())
	if err != nil || q.GetIdempotencyKey() == "" {
		return nil, fmt.Errorf("%w: status needs invoice_id and idempotency_key", workerx.ErrPoison)
	}
	prov, reply, err := h.providerForCommand(ctx, run, invoiceID, false, "", "")
	if err != nil {
		return nil, err
	}
	if reply != nil {
		return reply, nil
	}
	if _, err := h.Attempts.Start(ctx, run.id, run.tenant, invoiceID, "status", run.env, prov.Name(), q.GetIdempotencyKey(), q); err != nil {
		return nil, err
	}
	if mc, ok := prov.(payments.ManualCollector); ok {
		return h.manualStatus(ctx, run, invoiceID, q.GetIdempotencyKey(), mc)
	}
	// A charge that failed over was made at another account under another key: that is the one to ask about (the account is the invoice's last charge provider).
	key := q.GetIdempotencyKey()
	if h.Failovers != nil {
		if fo, ok, err := h.Attempts.FailoverAttempt(ctx, run.tenant, invoiceID, payments.FailoverPrefix(key)); err != nil {
			return nil, err
		} else if ok {
			key = fo.Key
		}
	}
	if !capabilities(prov).IdempotentCharges && h.Ledger.DB != nil {
		// A provider that settles by notification: the ledger holds the outcome once its notification has been confirmed.
		if f, ok, err := h.Ledger.ChargeFact(ctx, run.tenant, key); err != nil {
			return nil, err
		} else if ok {
			return statusFromLedger(f), nil
		}
	}
	st, err := h.providerStatus(ctx, run, invoiceID, prov, key)
	if err != nil {
		return nil, err // a failed query (or "still pending") is retried; billing keeps the saga in PAYMENT_UNKNOWN meanwhile
	}
	return statusFromProvider(st), nil
}

// providerStatus asks the provider how a charge went: by the reference the first response gave when the adapter can be asked that way and the attempt has one (a Stripe
// PaymentIntent is read where it is, not searched for), else by the key. A payment that waits for the customer to authenticate is pending until the window of the pending
// payments (a bank transfer's, PAYMENTS_TRANSFER_WINDOW_DAYS: default, not agreed) has passed since the charge, then it is declined as authentication_required and
// dunning applies, so a customer who never comes back does not keep the invoice waiting for ever.
func (h *Handler) providerStatus(ctx context.Context, run commandRun, invoice uuid.UUID, prov payments.PaymentProvider, key string) (payments.StatusResult, error) {
	ref, started, found, err := h.Attempts.ChargeRef(ctx, run.tenant, invoice, key)
	if err != nil {
		return payments.StatusResult{}, err
	}
	var st payments.StatusResult
	if rs, ok := prov.(payments.RefStatuser); ok && ref != "" {
		st, err = rs.StatusByRef(ctx, ref, key)
	} else {
		st, err = prov.Status(ctx, key)
	}
	if err != nil {
		return st, err
	}
	if st.State == payments.StateActionRequired && found && h.TransferWindow > 0 && h.now().Sub(started) > h.TransferWindow {
		return payments.StatusResult{State: payments.StateDeclined, ChargeID: st.ChargeID, DeclineCode: "authentication_required"}, nil
	}
	return st, nil
}

func statusFromLedger(f store.Fact) *commandsv1.ReplyEnvelope {
	reply := &commandsv1.PaymentStatus{PspChargeId: f.ProviderRef}
	if f.Status == store.StatusSucceeded {
		reply.State = commandsv1.PaymentStatus_STATE_SUCCEEDED
	} else {
		reply.State, reply.DeclineCode = commandsv1.PaymentStatus_STATE_DECLINED, f.Detail
	}
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: reply}}
}

func statusFromProvider(st payments.StatusResult) *commandsv1.ReplyEnvelope {
	reply := &commandsv1.PaymentStatus{PspChargeId: st.ChargeID, DeclineCode: st.DeclineCode}
	switch st.State {
	case payments.StateSucceeded:
		reply.State = commandsv1.PaymentStatus_STATE_SUCCEEDED
	case payments.StateDeclined:
		reply.State, reply.Final = commandsv1.PaymentStatus_STATE_DECLINED, st.Final
	case payments.StateActionRequired:
		reply.State = commandsv1.PaymentStatus_STATE_ACTION_REQUIRED
	default:
		reply.State = commandsv1.PaymentStatus_STATE_NOT_FOUND
	}
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: reply}}
}
