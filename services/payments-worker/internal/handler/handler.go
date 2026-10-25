// Package handler executes payment commands against a PaymentProvider and translates the outcome into replies.
// It holds no money logic: amounts come from the command.
package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Handler routes commands by environment (ADR 0011) and, for live tenants, by their provider account.
type Handler struct {
	// Sandbox is always mock-psp: a sandbox command can never reach a real provider, whatever the configuration says.
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
func (h *Handler) providerFor(ctx context.Context, tenant uuid.UUID, env plansv1.Environment, invoice uuid.UUID, charge bool, named string) (payments.PaymentProvider, error) {
	if h.Demo || env == plansv1.Environment_ENVIRONMENT_SANDBOX {
		return h.Sandbox, nil
	}
	if h.Resolver != nil {
		if charge && named != "" {
			// The subscription names its provider (it was routed there): that account, not the tenant's default.
			if h.Live != nil && h.Live.Name() == named {
				return h.Live, nil
			}
			return h.Resolver.Named(ctx, tenant, named, "live")
		}
		if charge {
			p, ok, err := h.Resolver.Default(ctx, tenant, "live")
			if err != nil {
				return nil, err
			}
			if ok {
				return p, nil
			}
		} else if name, ok, err := h.Attempts.ChargeProvider(ctx, tenant, invoice); err != nil {
			return nil, err
		} else if ok && (h.Live == nil || h.Live.Name() != name) {
			return h.Resolver.Named(ctx, tenant, name, "live")
		} else if !ok {
			// Never charged through us (an invoice settled elsewhere): the tenant's default account is the best guess.
			p, found, err := h.Resolver.Default(ctx, tenant, "live")
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
	return errored(false, CodeNotConfigured, err.Error())
}

func envName(env plansv1.Environment) string {
	if env == plansv1.Environment_ENVIRONMENT_SANDBOX {
		return "sandbox"
	}
	return "live"
}

// codeKeys are the error keys (docs/error-keys.md) of the codes PaymentErrored carries.
var codeKeys = map[string]string{
	CodeNotConfigured: errkey.Key("payments.charge.provider_not_configured"),
	CodeTimeout:       errkey.Key("payments.charge.timeout"),
	CodeRejected:      errkey.Key("payments.charge.rejected"),
	CodeCurrency:      errkey.Key("payments.charge.currency_not_supported"),
	CodePending:       errkey.Key("payments.charge.pending"),
}

func errored(retryable bool, code, msg string) *commandsv1.ReplyEnvelope {
	key, ok := codeKeys[code]
	if !ok {
		key = errkey.Default(codes.Internal)
	}
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentErrored{PaymentErrored: &commandsv1.PaymentErrored{Retryable: retryable, Code: code, Message: msg, ErrorKey: key}}}
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
	tenantID, _ := uuid.Parse(cmd.GetTenantId())
	run := commandRun{id: commandID, tenant: tenantID, environment: cmd.GetEnvironment(), env: envName(cmd.GetEnvironment())}

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
	environment plansv1.Environment
	env         string
}

// providerForCommand resolves the provider of a command; a non-nil reply is the answer when there is none.
func (h *Handler) providerForCommand(ctx context.Context, run commandRun, invoice uuid.UUID, charge bool, named string) (payments.PaymentProvider, *commandsv1.ReplyEnvelope) {
	prov, err := h.providerFor(ctx, run.tenant, run.environment, invoice, charge, named)
	if err != nil {
		return nil, unavailable(err)
	}
	if prov == nil {
		return nil, errored(false, CodeNotConfigured, "no live payment provider is configured")
	}
	return prov, nil
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
	prov, reply := h.providerForCommand(ctx, run, invoiceID, true, ch.GetProvider())
	if reply != nil {
		return reply, nil
	}
	caps := capabilities(prov)
	if !caps.CanCharge(ch.GetCurrency()) {
		return errored(false, CodeCurrency, fmt.Sprintf("%s cannot charge in %s", prov.Name(), ch.GetCurrency())), nil
	}
	redelivered, err := h.Attempts.Start(ctx, run.id, run.tenant, invoiceID, "charge", run.env, prov.Name(), ch.GetIdempotencyKey(), ch)
	if err != nil {
		return nil, err
	}
	entry := store.LedgerEntry{TenantID: run.tenant, InvoiceID: invoiceID, Provider: prov.Name(), Environment: run.env, Kind: "charge",
		AmountMinor: ch.GetAmountMinor(), Currency: ch.GetCurrency(), IdempotencyKey: ch.GetIdempotencyKey(), Source: "command"}
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
		entry.Status, entry.ProviderRef, entry.Detail, entry.OccurredAt = "pending", entry.IdempotencyKey, pending.Ref, h.now()
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
		entry.Status, entry.ProviderRef = "succeeded", st.ChargeID
		h.record(ctx, entry)
		return succeededReply(st.ChargeID), nil
	case payments.StateDeclined:
		entry.Status, entry.Detail = "declined", st.DeclineCode
		h.record(ctx, entry)
		return declinedReply(st.DeclineCode), nil
	}
	return nil, nil
}

func (h *Handler) chargeOutcome(ctx context.Context, entry store.LedgerEntry, res payments.ChargeResult, err error) (*commandsv1.ReplyEnvelope, error) {
	var declined *payments.DeclinedError
	var transient *payments.TransientError
	var pending *payments.PendingError
	switch {
	case errors.As(err, &pending):
		// Accepted, not settled: the provider's notification decides. Billing queries the status, which reads the ledger.
		return errored(false, CodePending, pending.Error()), nil
	case err == nil:
		entry.Status, entry.ProviderRef = "succeeded", res.ChargeID
		h.record(ctx, entry)
		return succeededReply(res.ChargeID), nil
	case errors.As(err, &declined):
		entry.Status, entry.Detail = "declined", declined.Code
		h.record(ctx, entry)
		return declinedReply(declined.Code), nil
	case errors.As(err, &transient):
		return nil, err // retried through the delay queues; a provider that does not dedupe is asked first
	case errors.Is(err, payments.ErrTimeout):
		// Never retried blindly: billing queries the status first.
		return errored(false, CodeTimeout, "the provider did not answer in time"), nil
	default:
		return errored(false, CodeRejected, err.Error()), nil
	}
}

func (h *Handler) refund(ctx context.Context, run commandRun, rf *commandsv1.RefundPayment) (*commandsv1.ReplyEnvelope, error) {
	invoiceID, err := uuid.Parse(rf.GetInvoiceId())
	if err != nil || rf.GetIdempotencyKey() == "" {
		return nil, fmt.Errorf("%w: refund needs invoice_id and idempotency_key", workerx.ErrPoison)
	}
	prov, reply := h.providerForCommand(ctx, run, invoiceID, false, "")
	if reply != nil {
		return reply, nil
	}
	if !capabilities(prov).Refunds {
		// The credit note then shows the refund as failed, with this reason: the money goes back outside Payrudder (for a bank transfer, by a transfer back).
		return errored(false, CodeRejected, prov.Name()+" has no refund API; refund the customer outside Payrudder"), nil
	}
	if _, err := h.Attempts.Start(ctx, run.id, run.tenant, invoiceID, "refund", run.env, prov.Name(), rf.GetIdempotencyKey(), rf); err != nil {
		return nil, err
	}
	res, err := prov.Refund(ctx, payments.RefundRequest{IdempotencyKey: rf.GetIdempotencyKey(), InvoiceID: rf.GetInvoiceId(),
		ChargeID: rf.GetPspChargeId(), AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency()})
	var transient *payments.TransientError
	switch {
	case err == nil:
		h.record(ctx, store.LedgerEntry{TenantID: run.tenant, InvoiceID: invoiceID, Provider: prov.Name(), Environment: run.env, Kind: "refund", Status: "succeeded",
			AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency(), ProviderRef: res.RefundID, IdempotencyKey: rf.GetIdempotencyKey(), Source: "command"})
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentRefunded{PaymentRefunded: &commandsv1.PaymentRefunded{PspRefundId: res.RefundID}}}, nil
	case errors.As(err, &transient):
		return nil, err
	case errors.Is(err, payments.ErrTimeout):
		return errored(false, CodeTimeout, "the provider did not answer in time"), nil
	default:
		return errored(false, CodeRejected, err.Error()), nil
	}
}

func (h *Handler) queryStatus(ctx context.Context, run commandRun, q *commandsv1.QueryPaymentStatus) (*commandsv1.ReplyEnvelope, error) {
	invoiceID, err := uuid.Parse(q.GetInvoiceId())
	if err != nil || q.GetIdempotencyKey() == "" {
		return nil, fmt.Errorf("%w: status needs invoice_id and idempotency_key", workerx.ErrPoison)
	}
	prov, reply := h.providerForCommand(ctx, run, invoiceID, false, "")
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
		if f, ok, err := h.Ledger.ChargeFact(ctx, q.GetIdempotencyKey()); err != nil {
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
	if f.Status == "succeeded" {
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
