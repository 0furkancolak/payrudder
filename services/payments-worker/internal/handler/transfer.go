package handler

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Decline codes of a bank transfer that was given up. The ledger keeps them as the start of the detail of the declined row ("code: reason").
const (
	TransferExpired  = "transfer_expired"
	TransferRejected = "transfer_rejected"
)

// DeclineCode is the code at the start of a closure's detail.
func DeclineCode(detail string) string {
	code, _, _ := strings.Cut(detail, ":")
	return strings.TrimSpace(code)
}

// statusReply is the answer to a status query: the state, and the decline code of a declined one.
func statusReply(state commandsv1.PaymentStatus_State, declineCode string) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PaymentStatus{PaymentStatus: &commandsv1.PaymentStatus{State: state, DeclineCode: declineCode}}}
}

// window is how long this account lets a transfer wait: its own setting, else the deployment's (PAYMENTS_TRANSFER_WINDOW_DAYS, which Config.Validate
// requires and the YAML defaults to 14).
func (h *Handler) window(mc payments.ManualCollector) time.Duration {
	if w := mc.PaymentWindow(); w > 0 {
		return w
	}
	return h.TransferWindow
}

// manualStatus answers a status query for a provider whose payments an operator confirms by hand. The ledger is the only source:
//
//   - the confirmed receipts of the invoice add up to the charge: SUCCEEDED;
//   - the charge was rejected or has expired: DECLINED, which starts the tenant's dunning;
//   - the payment window has passed since the customer was told to pay: the charge expires now (a declined row is written) and the answer is DECLINED;
//   - otherwise PENDING, so the saga waits and asks again later;
//   - a charge that never left a pending row is NOT_FOUND (the saga charges again, which only repeats the instructions).
func (h *Handler) manualStatus(ctx context.Context, run commandRun, invoice uuid.UUID, key string, mc payments.ManualCollector) (*commandsv1.ReplyEnvelope, error) {
	charge, ok, err := h.Attempts.ChargeByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return statusReply(commandsv1.PaymentStatus_STATE_NOT_FOUND, ""), nil
	}
	name := mc.Name()
	got, err := h.Transfers.Received(ctx, run.tenant, invoice, name)
	if err != nil {
		return nil, err
	}
	if charge.AmountMinor > 0 && got.Minor >= charge.AmountMinor {
		return statusFromLedger(store.Fact{Status: store.StatusSucceeded, ProviderRef: got.LastRef}), nil
	}
	if c, found, err := h.Transfers.Closure(ctx, name, key); err != nil {
		return nil, err
	} else if found {
		return statusReply(commandsv1.PaymentStatus_STATE_DECLINED, DeclineCode(c.Detail)), nil
	}
	p, found, err := h.Transfers.Pending(ctx, name, key)
	if err != nil {
		return nil, err
	}
	if !found {
		return statusReply(commandsv1.PaymentStatus_STATE_NOT_FOUND, ""), nil
	}
	if h.now().Sub(p.Since) <= h.window(mc) {
		return statusReply(commandsv1.PaymentStatus_STATE_PENDING, ""), nil
	}
	if _, err := h.Transfers.Close(ctx, store.ClosureEntry{TenantID: run.tenant, InvoiceID: invoice, Provider: name, Environment: run.env, AmountMinor: charge.AmountMinor,
		Currency: charge.Currency, IdempotencyKey: key, Source: store.SourceCommand, Detail: TransferExpired + ": no transfer within the payment window"}); err != nil {
		return nil, err
	}
	return statusReply(commandsv1.PaymentStatus_STATE_DECLINED, TransferExpired), nil
}
