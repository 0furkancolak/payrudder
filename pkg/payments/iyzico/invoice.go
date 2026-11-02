package iyzico

import (
	"context"
	"errors"
	"strings"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// checkoutResponse is the part of the checkout form answer that Payrudder reads.
type checkoutResponse struct {
	Status         string `json:"status"`
	ErrorCode      text   `json:"errorCode"`
	ErrorMessage   string `json:"errorMessage"`
	Token          string `json:"token"`
	PaymentPageURL string `json:"paymentPageUrl"`
}

// CreateInvoiceCheckout opens iyzico's hosted payment page (checkout form) for one invoice. The key is the conversationId, which iyzico hands back
// in its notification and which Status uses to find the payment.
// The payer comes back through the callback URL (a route of the gateway that posts here, ConfirmReturn); the notification settles the invoice too.
// UNVERIFIED: that a checkout-form payment can be looked up by conversationId through /payment/detail; the endpoint and the fields are those of cf-initialize
// (docs.iyzico.com/en/payment-methods/checkoutform/cf-implementation/cf-initialize, read 2026-11-02), no sandbox has run them.
func (c *Client) CreateInvoiceCheckout(ctx context.Context, r payments.CheckoutInvoiceRequest) (payments.Checkout, error) {
	if r.IdempotencyKey == "" || r.ReturnURL == "" && r.CallbackURL == "" {
		return payments.Checkout{}, errors.New("iyzico: an invoice checkout needs a key and a callback url")
	}
	amount, err := price(r.AmountMinor, r.Currency)
	if err != nil {
		return payments.Checkout{}, err
	}
	buyer := r.Buyer
	if buyer == nil {
		// A guest: all there is is an email and a name. The seller's defaults fill what they configured, and what stays missing is named.
		buyer = &payments.Buyer{Email: r.CustomerEmail, Name: r.CustomerName}
	}
	b, addr, err := c.buyerObjects(buyer, r.InvoiceID)
	if err != nil {
		return payments.Checkout{}, err
	}
	callback := r.CallbackURL
	if callback == "" {
		callback = r.ReturnURL
	}
	installments := c.s.EnabledInstallments
	if len(installments) == 0 {
		installments = []int{1}
	}
	req := map[string]any{
		"locale": "en", "conversationId": r.IdempotencyKey, "price": amount, "paidPrice": amount, "currency": r.Currency,
		"basketId": r.InvoiceID, "paymentGroup": "PRODUCT", "callbackUrl": callback, "enabledInstallments": installments,
		"buyer": b, "shippingAddress": addr, "billingAddress": addr,
		"basketItems": []map[string]string{{"id": r.InvoiceID, "name": "Invoice " + r.Number, "category1": "Invoice", "itemType": "VIRTUAL", "price": amount}},
	}
	var out checkoutResponse
	if err := c.doInto(ctx, "/payment/iyzipos/checkoutform/initialize/auth/ecom", req, &out); err != nil {
		return payments.Checkout{}, err
	}
	if !strings.EqualFold(out.Status, "success") {
		return payments.Checkout{}, c.classify(response{Status: out.Status, ErrorCode: out.ErrorCode, ErrorMessage: out.ErrorMessage})
	}
	if out.PaymentPageURL == "" {
		return payments.Checkout{}, errors.New("iyzico: the checkout form has no paymentPageUrl")
	}
	return payments.Checkout{URL: out.PaymentPageURL, Ref: out.Token}, nil
}
