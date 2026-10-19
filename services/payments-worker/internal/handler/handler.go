// Package handler executes payment commands against a PaymentProvider and translates the outcome into replies.
// It holds no money logic: amounts come from the command.
package handler

import (
	"context"
	"errors"
	"fmt"

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

// Handler routes commands by environment (ADR 0011).
type Handler struct {
	// Sandbox is always mock-psp: a sandbox command can never reach a real provider, whatever the configuration says.
	Sandbox payments.PaymentProvider
	// Live is PAYMENTS_LIVE_PROVIDER. Nil replies PaymentErrored{PROVIDER_NOT_CONFIGURED}.
	Live     payments.PaymentProvider
	Attempts store.Attempts
}

// Err codes carried by PaymentErrored.
const (
	CodeNotConfigured = "PROVIDER_NOT_CONFIGURED"
	CodeTimeout       = "TIMEOUT"
	CodeRejected      = "REJECTED"
)

func (h *Handler) provider(env plansv1.Environment) payments.PaymentProvider {
	if env == plansv1.Environment_ENVIRONMENT_SANDBOX {
		return h.Sandbox
	}
	return h.Live
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
	prov := h.provider(cmd.GetEnvironment())

	switch c := cmd.GetCommand().(type) {
	case *commandsv1.CommandEnvelope_ChargePayment:
		ch := c.ChargePayment
		invoiceID, err := uuid.Parse(ch.GetInvoiceId())
		if err != nil || ch.GetIdempotencyKey() == "" {
			return nil, fmt.Errorf("%w: charge needs invoice_id and idempotency_key", workerx.ErrPoison)
		}
		if prov == nil {
			return errored(false, CodeNotConfigured, "no live payment provider is configured"), nil
		}
		if err := h.Attempts.Start(ctx, commandID, tenantID, invoiceID, "charge", envName(cmd.GetEnvironment()), prov.Name(), ch.GetIdempotencyKey(), ch); err != nil {
			return nil, err
		}
		res, err := prov.Charge(ctx, payments.ChargeRequest{IdempotencyKey: ch.GetIdempotencyKey(), InvoiceID: ch.GetInvoiceId(),
			AmountMinor: ch.GetAmountMinor(), Currency: ch.GetCurrency(), PaymentMethod: ch.GetPaymentMethod()})
		var declined *payments.DeclinedError
		var transient *payments.TransientError
		switch {
		case err == nil:
			return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentSucceeded{PaymentSucceeded: &commandsv1.PaymentSucceeded{PspChargeId: res.ChargeID}}}, nil
		case errors.As(err, &declined):
			return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentDeclined{PaymentDeclined: &commandsv1.PaymentDeclined{DeclineCode: declined.Code}}}, nil
		case errors.As(err, &transient):
			return nil, err // retried through the delay queues; the same idempotency key keeps it safe
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
		if prov == nil {
			return errored(false, CodeNotConfigured, "no live payment provider is configured"), nil
		}
		if err := h.Attempts.Start(ctx, commandID, tenantID, invoiceID, "refund", envName(cmd.GetEnvironment()), prov.Name(), rf.GetIdempotencyKey(), rf); err != nil {
			return nil, err
		}
		res, err := prov.Refund(ctx, payments.RefundRequest{IdempotencyKey: rf.GetIdempotencyKey(), InvoiceID: rf.GetInvoiceId(),
			ChargeID: rf.GetPspChargeId(), AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency()})
		var transient *payments.TransientError
		switch {
		case err == nil:
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
		if prov == nil {
			return errored(false, CodeNotConfigured, "no live payment provider is configured"), nil
		}
		if err := h.Attempts.Start(ctx, commandID, tenantID, invoiceID, "status", envName(cmd.GetEnvironment()), prov.Name(), q.GetIdempotencyKey(), q); err != nil {
			return nil, err
		}
		st, err := prov.Status(ctx, q.GetIdempotencyKey())
		if err != nil {
			return nil, err // a failed query is retried; billing keeps the saga in PAYMENT_UNKNOWN meanwhile
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
