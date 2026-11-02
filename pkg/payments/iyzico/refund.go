package iyzico

import (
	"context"
	"errors"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// trt is Turkey's clock, which has been UTC+3 all year since 2016 and is the day iyzico's banks close on. UNVERIFIED: the documentation says a cancel works "within
// the same day that payment was received (before banks perform their own reconciliation)" and names no time zone or cut-off; a cancel the bank refuses after the
// cut-off is not an error here, the payment is refunded instead.
var trt = time.FixedZone("TRT", 3*60*60)

func sameDay(a, b time.Time) bool {
	return a.In(trt).Format(time.DateOnly) == b.In(trt).Format(time.DateOnly)
}

// Refund implements payments.PaymentProvider: the amount given, against the payment (ChargeID).
//
// Cancel or refund (docs.iyzico.com/en/advanced/refund-and-cancel, read 2026-11-02): a cancel (POST /payment/cancel, by paymentId) undoes the payment the day it was
// taken and leaves no entry on the card statement, but it is for the whole payment only; a refund (POST /v2/payment/refund, by paymentId and price) can be partial
// and is allowed for 365 days. So the whole payment, refunded the day it was taken (the request says when and how much: RefundRequest.ChargedAt), is cancelled first;
// everything else is refunded. A cancel iyzico answers with a failure that is not "try again later" (the bank's day has closed, a test card that cannot be cancelled)
// falls back to the refund, since nothing moved; a cancel whose outcome is unknown (no answer, a system error) is returned as it is, never followed by a refund, and the
// guard of payments-worker parks it. Neither call is idempotent, so the same refund is never sent twice.
//
// Per-item refunds (POST /payment/refund with the paymentTransactionId of a basket item) are not used: the page advises against the paymentId refund only "for orders
// with more than one product in the basket", and every basket Payrudder sends has exactly one item, so a partial refund by paymentId is the refund of that item.
func (c *Client) Refund(ctx context.Context, r payments.RefundRequest) (payments.RefundResult, error) {
	if r.ChargeID == "" {
		return payments.RefundResult{}, errors.New("iyzico: a refund needs the charge id (the paymentId)")
	}
	amount, err := price(r.AmountMinor, r.Currency)
	if err != nil {
		return payments.RefundResult{}, err
	}
	if r.AmountMinor > 0 && r.ChargeAmountMinor == r.AmountMinor && !r.ChargedAt.IsZero() && sameDay(r.ChargedAt, c.now()) {
		if res, done, err := c.cancel(ctx, r); done {
			return res, err
		}
	}
	out, err := c.do(ctx, "/v2/payment/refund", map[string]any{
		"locale": "en", "conversationId": r.IdempotencyKey, "paymentId": r.ChargeID, "price": amount, "currency": r.Currency, "ip": c.s.Buyer.IP,
	})
	if err != nil {
		return payments.RefundResult{}, err
	}
	if !out.ok() {
		return payments.RefundResult{}, c.classify(out)
	}
	id := out.RefundRef
	if id == "" {
		id = out.PaymentID
	}
	return payments.RefundResult{RefundID: id}, nil
}

// cancel asks iyzico to cancel the payment. done is false when iyzico said it did not and the payment may be refunded instead.
func (c *Client) cancel(ctx context.Context, r payments.RefundRequest) (res payments.RefundResult, done bool, err error) {
	out, err := c.do(ctx, "/payment/cancel", map[string]any{"locale": "en", "conversationId": r.IdempotencyKey, "paymentId": r.ChargeID, "ip": c.s.Buyer.IP})
	if err != nil {
		return payments.RefundResult{}, true, err
	}
	if !out.ok() {
		var transient *payments.TransientError
		if cerr := c.classify(out); errors.As(cerr, &transient) {
			return payments.RefundResult{}, true, cerr
		}
		return payments.RefundResult{}, false, nil
	}
	id := out.CancelRef
	if id == "" {
		id = out.PaymentID
	}
	if id == "" {
		id = r.ChargeID
	}
	return payments.RefundResult{RefundID: id}, true, nil
}
