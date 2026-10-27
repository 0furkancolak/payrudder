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
	// Now is the clock; nil is time.Now.
	Now func() time.Time
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
)

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
			return fmt.Errorf("%w: a sandbox command was routed to %q: %v", ErrWrongProvider, p.Name(), err)
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
	return payments.Capabilities{Name: p.Name(), Refunds: true, IdempotentCharges: true}
}

// record writes a ledger row. A failure is logged and never fails the command: the provider has already been called, and calling it
// again for the sake of a bookkeeping error could charge twice. The webhook or the reconciliation writes the row later.
func (h *Handler) record(ctx context.Context, e store.LedgerEntry) {
	if h.Ledger.DB == nil {
		return
	}
	if _, err := h.Ledger.Append(ctx, e); err != nil && h.Log != nil {
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

func declinedReply(code string) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentDeclined{PaymentDeclined: &commandsv1.PaymentDeclined{DeclineCode: code}}}
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
	redelivered, err := h.Attempts.Start(ctx, run.id, run.tenant, invoiceID, store.KindCharge, run.env, prov.Name(), ch.GetIdempotencyKey(), ch)
	if err != nil {
		return nil, err
	}
	entry := store.LedgerEntry{TenantID: run.tenant, InvoiceID: invoiceID, Provider: prov.Name(), Environment: run.env, Kind: store.KindCharge,
		AmountMinor: ch.GetAmountMinor(), Currency: ch.GetCurrency(), IdempotencyKey: ch.GetIdempotencyKey(), Source: store.SourceCommand}
	_, manual := prov.(payments.ManualCollector)
	if redelivered && !caps.IdempotentCharges && !manual {
		if reply, err := h.askBeforeCharging(ctx, prov, entry); reply != nil || err != nil {
			return reply, err
		}
	}
	res, err := prov.Charge(ctx, payments.ChargeRequest{IdempotencyKey: ch.GetIdempotencyKey(), InvoiceID: ch.GetInvoiceId(),
		AmountMinor: ch.GetAmountMinor(), Currency: ch.GetCurrency(), PaymentMethod: ch.GetPaymentMethod(), InvoiceNumber: ch.GetInvoiceNumber()})
	var pending *payments.PendingError
	if manual && errors.As(err, &pending) {
		// The customer has been told how to pay. The row marks when the payment window opened; its reference is the charge's key, so a dunning retry
		// (another key) opens a window of its own.
		entry.Status, entry.ProviderRef, entry.Detail, entry.OccurredAt = store.StatusPending, entry.IdempotencyKey, pending.Ref, h.now()
		h.record(ctx, entry)
	}
	return h.chargeOutcome(ctx, entry, res, err)
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
		return declinedReply(st.DeclineCode), nil
	}
	return nil, nil
}

func (h *Handler) chargeOutcome(ctx context.Context, entry store.LedgerEntry, res payments.ChargeResult, err error) (*commandsv1.ReplyEnvelope, error) {
	var declined *payments.DeclinedError
	var pending *payments.PendingError
	switch {
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
		return declinedReply(declined.Code), nil
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
	if !capabilities(prov).Refunds {
		// The credit note then shows the refund as failed, with this reason: the money goes back outside Payrudder (for a bank transfer, by a transfer back).
		return errored(CodeRejected, prov.Name()+" has no refund API; refund the customer outside Payrudder"), nil
	}
	if _, err := h.Attempts.Start(ctx, run.id, run.tenant, invoiceID, store.KindRefund, run.env, prov.Name(), rf.GetIdempotencyKey(), rf); err != nil {
		return nil, err
	}
	res, err := prov.Refund(ctx, payments.RefundRequest{IdempotencyKey: rf.GetIdempotencyKey(), InvoiceID: rf.GetInvoiceId(),
		ChargeID: rf.GetPspChargeId(), AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency()})
	if err != nil {
		return providerFailure(err)
	}
	h.record(ctx, store.LedgerEntry{TenantID: run.tenant, InvoiceID: invoiceID, Provider: prov.Name(), Environment: run.env, Kind: store.KindRefund, Status: store.StatusSucceeded,
		AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency(), ProviderRef: res.RefundID, IdempotencyKey: rf.GetIdempotencyKey(), Source: store.SourceCommand})
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentRefunded{PaymentRefunded: &commandsv1.PaymentRefunded{PspRefundId: res.RefundID}}}, nil
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
	if !capabilities(prov).IdempotentCharges && h.Ledger.DB != nil {
		// A provider that settles by notification: the ledger holds the outcome once its notification has been confirmed.
		if f, ok, err := h.Ledger.ChargeFact(ctx, run.tenant, q.GetIdempotencyKey()); err != nil {
			return nil, err
		} else if ok {
			return statusFromLedger(f), nil
		}
	}
	st, err := prov.Status(ctx, q.GetIdempotencyKey())
	if err != nil {
		return nil, err // a failed query (or "still pending") is retried; billing keeps the saga in PAYMENT_UNKNOWN meanwhile
	}
	return statusFromProvider(st), nil
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
		reply.State = commandsv1.PaymentStatus_STATE_DECLINED
	default:
		reply.State = commandsv1.PaymentStatus_STATE_NOT_FOUND
	}
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: reply}}
}
