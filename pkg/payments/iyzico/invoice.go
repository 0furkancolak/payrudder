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
	ErrorCode      string `json:"errorCode"`
	ErrorMessage   string `json:"errorMessage"`
	Token          string `json:"token"`
	PaymentPageURL string `json:"paymentPageUrl"`
}

// CreateInvoiceCheckout opens iyzico's hosted payment page (checkout form) for one invoice. The key is the conversationId, which iyzico hands back
// in its notification and which Status uses to find the payment.
// UNVERIFIED: the endpoint /payment/iyzipos/checkoutform/initialize/auth/ecom, the field names and that a checkout-form payment can be looked up by
// conversationId through /payment/detail were written from iyzico's public documentation; no sandbox has run them.
func (c *Client) CreateInvoiceCheckout(ctx context.Context, r payments.CheckoutInvoiceRequest) (payments.Checkout, error) {
	if r.IdempotencyKey == "" || r.ReturnURL == "" {
		return payments.Checkout{}, errors.New("iyzico: an invoice checkout needs a key and a return url")
	}
	amount, err := price(r.AmountMinor, r.Currency)
	if err != nil {
		return payments.Checkout{}, err
	}
	b := c.s.Buyer
	name, surname := b.Name, b.Surname
	if r.CustomerName != "" {
		name, surname, _ = strings.Cut(r.CustomerName, " ")
		if surname == "" {
			surname = b.Surname
		}
	}
	email := b.Email
	if r.CustomerEmail != "" {
		email = r.CustomerEmail
	}
	addr := map[string]string{"address": b.Address, "zipCode": b.ZipCode, "contactName": name + " " + surname, "city": b.City, "country": b.Country}
	req := map[string]any{
		"locale": "en", "conversationId": r.IdempotencyKey, "price": amount, "paidPrice": amount, "currency": r.Currency,
		"basketId": r.InvoiceID, "paymentGroup": "PRODUCT", "callbackUrl": r.ReturnURL, "enabledInstallments": []int{1},
		"buyer": map[string]string{
			"id": r.InvoiceID, "name": name, "surname": surname, "identityNumber": b.IdentityNumber, "email": email,
			"gsmNumber": b.GSMNumber, "registrationAddress": b.Address, "city": b.City, "country": b.Country, "zipCode": b.ZipCode, "ip": b.IP,
		},
		"shippingAddress": addr, "billingAddress": addr,
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
