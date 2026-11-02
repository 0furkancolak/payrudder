package handler

import (
	"context"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
)

// refundRequest is what the provider is sent for a refund: the command, and what the payment being refunded was (its amount and when it was taken) as the ledger holds
// it, for a provider that cancels a payment taken the same day instead of refunding it (iyzico). A ledger that cannot say sends neither: the provider refunds, as it
// always did.
func (h *Handler) refundRequest(ctx context.Context, run commandRun, invoice uuid.UUID, rf *commandsv1.RefundPayment) payments.RefundRequest {
	r := payments.RefundRequest{IdempotencyKey: rf.GetIdempotencyKey(), InvoiceID: rf.GetInvoiceId(), ChargeID: rf.GetPspChargeId(), AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency()}
	if h.Ledger.DB == nil || r.ChargeID == "" {
		return r
	}
	if amount, at, found, err := h.Ledger.ChargeOfRef(ctx, run.tenant, invoice, r.ChargeID); err == nil && found {
		r.ChargeAmountMinor, r.ChargedAt = amount, at
	}
	return r
}
