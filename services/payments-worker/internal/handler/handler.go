// Package handler executes payment commands against a PaymentProvider and translates the outcome into replies.
// It holds no money logic: amounts come from the command.
package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/pkg/workerx"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
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
func (h *Handler) providerFor(ctx context.Context, tenant uuid.UUID, env plansv1.Environment, invoice uuid.UUID, charge bool) (payments.PaymentProvider, error) {
	if env == plansv1.Environment_ENVIRONMENT_SANDBOX {
		return h.Sandbox, nil
	}
	if h.Resolver != nil {
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

func errored(retryable bool, code, msg string) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentErrored{PaymentErrored: &commandsv1.PaymentErrored{Retryable: retryable, Code: code, Message: msg}}}
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
		default:
			result = "other"
		}
	}
	paymentsTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
}

var paymentsTotal = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/quotify/services/payments-worker").Int64Counter("payments",
		metric.WithDescription("Payment commands handled, by result"))
	if err != nil {
		panic(err)
	}
	return c
}()

func (h *Handler) handle(ctx context.Context, cmd *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
	commandID, err := uuid.Parse(cmd.GetCommandId())
	if err != nil {
		return nil, fmt.Errorf("%w: command_id", workerx.ErrPoison)
	}
	tenantID, _ := uuid.Parse(cmd.GetTenantId())
	env := envName(cmd.GetEnvironment())

	switch c := cmd.GetCommand().(type) {
	case *commandsv1.CommandEnvelope_ChargePayment:
		ch := c.ChargePayment
		invoiceID, err := uuid.Parse(ch.GetInvoiceId())
		if err != nil || ch.GetIdempotencyKey() == "" {
			return nil, fmt.Errorf("%w: charge needs invoice_id and idempotency_key", workerx.ErrPoison)
		}
		prov, err := h.providerFor(ctx, tenantID, cmd.GetEnvironment(), invoiceID, true)
		if err != nil {
			return unavailable(err), nil
		}
		if prov == nil {
			return errored(false, CodeNotConfigured, "no live payment provider is configured"), nil
		}
		caps := capabilities(prov)
		if !caps.CanCharge(ch.GetCurrency()) {
			return errored(false, CodeCurrency, fmt.Sprintf("%s cannot charge in %s", prov.Name(), ch.GetCurrency())), nil
		}
		redelivered, err := h.Attempts.Start(ctx, commandID, tenantID, invoiceID, "charge", env, prov.Name(), ch.GetIdempotencyKey(), ch)
		if err != nil {
			return nil, err
		}
		entry := store.LedgerEntry{TenantID: tenantID, InvoiceID: invoiceID, Provider: prov.Name(), Environment: env, Kind: "charge",
			AmountMinor: ch.GetAmountMinor(), Currency: ch.GetCurrency(), IdempotencyKey: ch.GetIdempotencyKey(), Source: "command"}
		if redelivered && !caps.IdempotentCharges {
			// The provider may have been called before the crash and does not dedupe by key: ask it, never charge blindly.
			st, err := prov.Status(ctx, ch.GetIdempotencyKey())
			if err != nil {
				return nil, err
			}
			switch st.State {
			case payments.StateSucceeded:
				entry.Status, entry.ProviderRef = "succeeded", st.ChargeID
				h.record(ctx, entry)
				return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentSucceeded{PaymentSucceeded: &commandsv1.PaymentSucceeded{PspChargeId: st.ChargeID}}}, nil
			case payments.StateDeclined:
				entry.Status, entry.Detail = "declined", st.DeclineCode
				h.record(ctx, entry)
				return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentDeclined{PaymentDeclined: &commandsv1.PaymentDeclined{DeclineCode: st.DeclineCode}}}, nil
			}
		}
		res, err := prov.Charge(ctx, payments.ChargeRequest{IdempotencyKey: ch.GetIdempotencyKey(), InvoiceID: ch.GetInvoiceId(),
			AmountMinor: ch.GetAmountMinor(), Currency: ch.GetCurrency(), PaymentMethod: ch.GetPaymentMethod()})
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
			return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentSucceeded{PaymentSucceeded: &commandsv1.PaymentSucceeded{PspChargeId: res.ChargeID}}}, nil
		case errors.As(err, &declined):
			entry.Status, entry.Detail = "declined", declined.Code
			h.record(ctx, entry)
			return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentDeclined{PaymentDeclined: &commandsv1.PaymentDeclined{DeclineCode: declined.Code}}}, nil
		case errors.As(err, &transient):
			return nil, err // retried through the delay queues; a provider that does not dedupe is asked first (above)
		case errors.Is(err, payments.ErrTimeout):
			// Never retried blindly: billing queries the status first.
			return errored(false, CodeTimeout, "the provider did not answer in time"), nil
		default:
			return errored(false, CodeRejected, err.Error()), nil
		}

	case *commandsv1.CommandEnvelope_RefundPayment:
		rf := c.RefundPayment
		invoiceID, err := uuid.Parse(rf.GetInvoiceId())
		if err != nil || rf.GetIdempotencyKey() == "" {
			return nil, fmt.Errorf("%w: refund needs invoice_id and idempotency_key", workerx.ErrPoison)
		}
		prov, err := h.providerFor(ctx, tenantID, cmd.GetEnvironment(), invoiceID, false)
		if err != nil {
			return unavailable(err), nil
		}
		if prov == nil {
			return errored(false, CodeNotConfigured, "no live payment provider is configured"), nil
		}
		if !capabilities(prov).Refunds {
			return errored(false, CodeRejected, prov.Name()+" has no refund API"), nil
		}
		if _, err := h.Attempts.Start(ctx, commandID, tenantID, invoiceID, "refund", env, prov.Name(), rf.GetIdempotencyKey(), rf); err != nil {
			return nil, err
		}
		res, err := prov.Refund(ctx, payments.RefundRequest{IdempotencyKey: rf.GetIdempotencyKey(), InvoiceID: rf.GetInvoiceId(),
			ChargeID: rf.GetPspChargeId(), AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency()})
		var transient *payments.TransientError
		switch {
		case err == nil:
			h.record(ctx, store.LedgerEntry{TenantID: tenantID, InvoiceID: invoiceID, Provider: prov.Name(), Environment: env, Kind: "refund", Status: "succeeded",
				AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency(), ProviderRef: res.RefundID, IdempotencyKey: rf.GetIdempotencyKey(), Source: "command"})
			return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentRefunded{PaymentRefunded: &commandsv1.PaymentRefunded{PspRefundId: res.RefundID}}}, nil
		case errors.As(err, &transient):
			return nil, err
		case errors.Is(err, payments.ErrTimeout):
			return errored(false, CodeTimeout, "the provider did not answer in time"), nil
		default:
			return errored(false, CodeRejected, err.Error()), nil
		}

	case *commandsv1.CommandEnvelope_QueryPaymentStatus:
		q := c.QueryPaymentStatus
		invoiceID, err := uuid.Parse(q.GetInvoiceId())
		if err != nil || q.GetIdempotencyKey() == "" {
			return nil, fmt.Errorf("%w: status needs invoice_id and idempotency_key", workerx.ErrPoison)
		}
		prov, err := h.providerFor(ctx, tenantID, cmd.GetEnvironment(), invoiceID, false)
		if err != nil {
			return unavailable(err), nil
		}
		if prov == nil {
			return errored(false, CodeNotConfigured, "no live payment provider is configured"), nil
		}
		if _, err := h.Attempts.Start(ctx, commandID, tenantID, invoiceID, "status", env, prov.Name(), q.GetIdempotencyKey(), q); err != nil {
			return nil, err
		}
		if !capabilities(prov).IdempotentCharges && h.Ledger.DB != nil {
			// A provider that settles by notification: the ledger holds the outcome once its notification has been confirmed.
			if f, ok, err := h.Ledger.ChargeFact(ctx, q.GetIdempotencyKey()); err != nil {
				return nil, err
			} else if ok {
				reply := &commandsv1.PaymentStatus{PspChargeId: f.ProviderRef}
				if f.Status == "succeeded" {
					reply.State = commandsv1.PaymentStatus_STATE_SUCCEEDED
				} else {
					reply.State, reply.DeclineCode = commandsv1.PaymentStatus_STATE_DECLINED, f.Detail
				}
				return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: reply}}, nil
			}
		}
		st, err := prov.Status(ctx, q.GetIdempotencyKey())
		if err != nil {
			return nil, err // a failed query (or "still pending") is retried; billing keeps the saga in PAYMENT_UNKNOWN meanwhile
		}
		reply := &commandsv1.PaymentStatus{PspChargeId: st.ChargeID, DeclineCode: st.DeclineCode}
		switch st.State {
		case payments.StateSucceeded:
			reply.State = commandsv1.PaymentStatus_STATE_SUCCEEDED
		case payments.StateDeclined:
			reply.State = commandsv1.PaymentStatus_STATE_DECLINED
		default:
			reply.State = commandsv1.PaymentStatus_STATE_NOT_FOUND
		}
		return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: reply}}, nil
	}
	return nil, fmt.Errorf("%w: not a payment command", workerx.ErrPoison)
}
