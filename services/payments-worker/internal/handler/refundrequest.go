package handler

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
)

// refundRequest is what the provider is sent for a refund: the command, and what the payment being refunded was (its amount and when it was taken) as the ledger holds
// it, for a provider that cancels a payment taken the same day instead of refunding it (iyzico). A ledger that cannot say sends neither: the provider refunds, as it
// always did.
func (h *Handler) refundRequest(ctx context.Context, run commandRun, invoice uuid.UUID, rf *commandsv1.RefundPayment, chargeID string) payments.RefundRequest {
	r := payments.RefundRequest{IdempotencyKey: rf.GetIdempotencyKey(), InvoiceID: rf.GetInvoiceId(), ChargeID: chargeID, AmountMinor: rf.GetAmountMinor(), Currency: rf.GetCurrency()}
	if h.Ledger.DB == nil || r.ChargeID == "" {
		return r
	}
	if amount, at, found, err := h.Ledger.ChargeOfRef(ctx, run.tenant, invoice, r.ChargeID); err == nil && found {
		r.ChargeAmountMinor, r.ChargedAt = amount, at
	}
	return r
}

// refundCharge is the provider's id of the charge a refund is made against. Billing keeps no charge id and sends none, and Stripe, Paddle and iyzico each refuse a refund
// that names no charge, so when the command has none the ledger is asked: the invoice's successful charge at the account that took it. With several (a payment link that
// was paid as well, a second payment) it takes the one whose amount the refund's is, and nothing else is guessed: a reply is returned instead, the provider is not asked and
// nothing moved. With none in the ledger the id stays empty, and the adapter says what it needs. mock-psp refunds by invoice and needs no id.
func (h *Handler) refundCharge(ctx context.Context, run commandRun, prov payments.PaymentProvider, invoice uuid.UUID, rf *commandsv1.RefundPayment) (string, *commandsv1.ReplyEnvelope, error) {
	if id := rf.GetPspChargeId(); id != "" || h.Ledger.DB == nil || IsMock(prov) {
		return id, nil, nil
	}
	ref, hasRef := h.account(prov)
	charges, err := h.Ledger.InvoiceCharges(ctx, run.tenant, invoice, accountName(prov, ref, hasRef))
	if err != nil {
		return "", nil, err
	}
	if len(charges) <= 1 {
		for _, c := range charges {
			return c.ProviderRef, nil, nil
		}
		return "", nil, nil
	}
	var same []string
	for _, c := range charges {
		if c.AmountMinor == rf.GetAmountMinor() {
			same = append(same, c.ProviderRef)
		}
	}
	if len(same) == 1 {
		return same[0], nil, nil
	}
	return "", errored(CodeRefundChargeUnclear, fmt.Sprintf("invoice %s has %d successful charges at %s and a refund of %d does not single out one of them (%d have that amount); refund the customer at %s, then record it on the credit note (Resolve refund)",
		invoice, len(charges), prov.Name(), rf.GetAmountMinor(), len(same), prov.Name())), nil
}
