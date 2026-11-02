package iyzico

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// threeDSInitPath and threeDSAuthPath are the two calls of a 3-D Secure payment (docs.iyzico.com/en/payment-methods/api/3ds/3ds-implementation/init-3ds and
// auth-3ds, read 2026-11-02): initialise, which returns the bank's page as base64 HTML, and, after the payer authenticated and iyzico posted the result to the callback
// URL, the auth call that finalises the payment. The page recommends the v2 auth call; it takes the paymentId, the paidPrice, the basketId and the currency of the
// initialise call.
const (
	threeDSInitPath = "/payment/3dsecure/initialize"
	threeDSAuthPath = "/payment/v2/3dsecure/auth"
)

// StoresPaymentMethod implements payments.ThreeDSProvider: a payment method token of the form "cardUserKey:cardToken".
func (c *Client) StoresPaymentMethod(pm string) bool {
	_, _, err := SplitPaymentMethod(pm)
	return err == nil
}

// StartThreeDS implements payments.ThreeDSProvider: it initialises a 3-D Secure payment with the customer's stored card (cardUserKey and cardToken, which
// docs.iyzico.com/en/advanced/card-storage says a 3DS payment request may use instead of the card details) and returns the page the payer must be shown. No money
// moves until the payer authenticated and ConfirmReturn has finalised the payment.
//
// The initialise request of the documentation takes the card number, expiry and CVC; Payrudder never holds them, so this call exists only for a stored card.
// UNVERIFIED against an iyzico account: that the 3-D Secure initialise call accepts paymentCard{cardUserKey, cardToken} (the card storage page says so in prose and
// shows the shape for /payment/auth only). Initialising twice is harmless: each call opens its own payment, and only an auth call takes money.
func (c *Client) StartThreeDS(ctx context.Context, r payments.ThreeDSRequest) (payments.ThreeDSStart, error) {
	if r.IdempotencyKey == "" || r.CallbackURL == "" {
		return payments.ThreeDSStart{}, errors.New("iyzico: a 3-D Secure payment needs a key and a callback url")
	}
	userKey, token, err := SplitPaymentMethod(r.PaymentMethod)
	if err != nil {
		return payments.ThreeDSStart{}, err
	}
	amount, err := price(r.AmountMinor, r.Currency)
	if err != nil {
		return payments.ThreeDSStart{}, err
	}
	paid, installment := amount, 1
	if r.Installments > 1 {
		// What the payer was quoted for this many instalments (the price and the bank's charge for them) is the paidPrice; the price stays the invoice's.
		if r.PaidMinor < r.AmountMinor || !slices.Contains(installmentCounts, r.Installments) {
			return payments.ThreeDSStart{}, fmt.Errorf("iyzico: %d instalments of %d (the quote is %d) cannot be started", r.Installments, r.AmountMinor, r.PaidMinor)
		}
		if paid, err = price(r.PaidMinor, r.Currency); err != nil {
			return payments.ThreeDSStart{}, err
		}
		installment = r.Installments
	}
	buyer, addr, err := c.buyerObjects(r.Buyer, r.InvoiceID)
	if err != nil {
		return payments.ThreeDSStart{}, err
	}
	out, err := c.do(ctx, threeDSInitPath, map[string]any{
		"locale": "en", "conversationId": r.IdempotencyKey, "price": amount, "paidPrice": paid, "currency": r.Currency, "installment": installment,
		"basketId": r.InvoiceID, "paymentChannel": "WEB", "paymentGroup": "PRODUCT", "callbackUrl": r.CallbackURL,
		"paymentCard": map[string]string{"cardUserKey": userKey, "cardToken": token},
		"buyer":       buyer, "shippingAddress": addr, "billingAddress": addr,
		"basketItems": []map[string]string{{"id": r.InvoiceID, "name": "Invoice " + r.Number, "category1": "Invoice", "itemType": "VIRTUAL", "price": amount}},
	})
	if err != nil {
		return payments.ThreeDSStart{}, err
	}
	if !out.ok() {
		return payments.ThreeDSStart{}, c.classify(out)
	}
	if out.PaymentID == "" || out.ThreeDSHTML == "" {
		return payments.ThreeDSStart{}, errors.New("iyzico: the 3-D Secure initialisation has no paymentId or page")
	}
	page, err := base64.StdEncoding.DecodeString(out.ThreeDSHTML)
	if err != nil {
		return payments.ThreeDSStart{}, fmt.Errorf("iyzico: the 3-D Secure page is not base64: %w", err)
	}
	return payments.ThreeDSStart{HTML: string(page), Ref: out.PaymentID}, nil
}

// confirmThreeDS finalises the payment the payer authenticated. The form is the payer's browser's word (docs 3ds-implementation, "Redirection": status, paymentId,
// conversationData, conversationId, mdStatus): it says whether the bank approved the authentication, and it is acted on only so far: an authentication that failed
// ends the payment without an auth call (mdStatus 1 is the only success), one that succeeded is finalised by the auth call, and what the payment is comes from
// iyzico's answer. The auth call is made once. When it fails, or its answer is lost, iyzico is asked what the payment is (/payment/detail) before anything is said
// about it, because a repeated return (the payer reloads the posted page) makes the auth call of a payment that is already finished.
func (c *Client) confirmThreeDS(ctx context.Context, r payments.ReturnRequest, paymentID string) (payments.StatusResult, error) {
	invoice, ok := invoiceOfKey(r.IdempotencyKey)
	if !ok {
		return payments.StatusResult{}, fmt.Errorf("%w: the conversation id %q is not one of Payrudder's charges", payments.ErrUnverified, r.IdempotencyKey)
	}
	if r.ProviderRef != "" && paymentID != r.ProviderRef {
		return payments.StatusResult{}, fmt.Errorf("%w: the return names payment %s, the attempt started %s", payments.ErrUnverified, paymentID, r.ProviderRef)
	}
	if md := r.Form.Get("mdStatus"); md != "" && md != "1" || strings.EqualFold(r.Form.Get("status"), "failure") {
		return payments.StatusResult{State: payments.StateDeclined, DeclineCode: "authentication_failed"}, nil
	}
	paidMinor := r.AmountMinor
	if r.PaidMinor > r.AmountMinor {
		paidMinor = r.PaidMinor // the attempt was split into instalments: the auth call names what the initialise call did
	}
	amount, err := price(paidMinor, r.Currency)
	if err != nil {
		return payments.StatusResult{}, err
	}
	out, err := c.do(ctx, threeDSAuthPath, map[string]any{
		"locale": "en", "conversationId": r.IdempotencyKey, "paymentId": paymentID, "paidPrice": amount, "basketId": invoice, "currency": r.Currency,
	})
	if err != nil {
		// No answer: the payment may be finished. What iyzico says it is, if it says, is the outcome.
		st, serr := c.detail(ctx, r.IdempotencyKey, paymentID, invoice)
		if serr == nil {
			return st, nil
		}
		var pending *payments.PendingError
		if errors.As(serr, &pending) {
			return payments.StatusResult{}, serr
		}
		return payments.StatusResult{}, err
	}
	if !out.ok() {
		// A refused auth call is a decline, unless the payment is in fact done (this return is a repeat): only a payment that iyzico says succeeded or holds for review
		// overrules the answer, since a failed payment's own answer names the reason.
		st, serr := c.detail(ctx, r.IdempotencyKey, paymentID, invoice)
		var pending *payments.PendingError
		if serr == nil && st.State == payments.StateSucceeded || errors.As(serr, &pending) {
			return st, serr
		}
		return declineOrError(c.classify(out))
	}
	if out.PaymentID != "" && out.PaymentID != paymentID || out.BasketID != "" && out.BasketID != invoice {
		return payments.StatusResult{}, fmt.Errorf("%w: the auth answer is for payment %s of basket %q", payments.ErrUnverified, out.PaymentID, out.BasketID)
	}
	if err := checkPrice(out, r); err != nil {
		return payments.StatusResult{}, err
	}
	if md := string(out.MdStatus); md != "" && md != "1" {
		return payments.StatusResult{State: payments.StateDeclined, DeclineCode: "authentication_failed"}, nil
	}
	out.PaymentID = paymentID
	out.PaymentStatus = "SUCCESS" // an auth call that succeeded finalises the payment; /payment/auth answers carry no paymentStatus either
	return out.state()
}

// detail asks iyzico what the payment is and keeps the answer only if it is final and the payment's basket is the invoice of the key.
func (c *Client) detail(ctx context.Context, key, paymentID, invoice string) (payments.StatusResult, error) {
	out, err := c.do(ctx, "/payment/detail", map[string]any{"locale": "en", "conversationId": key, "paymentId": paymentID})
	if err != nil {
		return payments.StatusResult{}, err
	}
	if !out.ok() {
		return payments.StatusResult{}, fmt.Errorf("iyzico: cannot look up payment %s: %s %s", paymentID, out.ErrorCode, out.ErrorMessage)
	}
	if out.BasketID != invoice {
		return payments.StatusResult{}, fmt.Errorf("%w: payment %s is not a payment of conversation %q", payments.ErrUnverified, paymentID, key)
	}
	return out.state()
}

// declineOrError reports a classified failure as a decline when it is one, and as the error it is otherwise (a system failure is not a decline of the card).
func declineOrError(err error) (payments.StatusResult, error) {
	var d *payments.DeclinedError
	if errors.As(err, &d) {
		return payments.StatusResult{State: payments.StateDeclined, DeclineCode: d.Code}, nil
	}
	return payments.StatusResult{}, err
}
