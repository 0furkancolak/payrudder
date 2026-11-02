package iyzico

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/payments"
)

// ConfirmReturn implements payments.ReturnConfirmer: the payer comes back from iyzico's page and the browser posts what iyzico gave it to the callback URL
// (docs.iyzico.com/en/payment-methods/checkoutform/cf-implementation/cf-retrieve: "a token value will be posted by iyzico"; 3ds-implementation "Redirection": status,
// paymentId, conversationData, conversationId, mdStatus). A token is a checkout form payment, a paymentId a 3-D Secure one. Neither form is believed: the payment is
// asked of iyzico, and must be the attempt's own (its basket is the invoice of the key, its token or payment id is the one the attempt was started with, its price is the
// attempt's amount). UNVERIFIED: that iyzico posts form-encoded fields (the pages do not name the encoding) and that it keeps the path of the callback URL it was given.
func (c *Client) ConfirmReturn(ctx context.Context, r payments.ReturnRequest) (payments.StatusResult, error) {
	if token := r.Form.Get("token"); token != "" {
		return c.confirmCheckoutForm(ctx, r, token)
	}
	if id := r.Form.Get("paymentId"); id != "" {
		return c.confirmThreeDS(ctx, r, id)
	}
	return payments.StatusResult{}, fmt.Errorf("%w: the return carries neither a token nor a paymentId", payments.ErrUnverified)
}

// confirmCheckoutForm retrieves the checkout form payment of a token (CF-Retrieve). A failure answer of a bank or fraud code is the payer's decline; the other
// failures are not, and a payment with no final state is an error, never a guess (response.state).
func (c *Client) confirmCheckoutForm(ctx context.Context, r payments.ReturnRequest, token string) (payments.StatusResult, error) {
	invoice, ok := invoiceOfKey(r.IdempotencyKey)
	if !ok {
		return payments.StatusResult{}, fmt.Errorf("%w: the conversation id %q is not one of Payrudder's charges", payments.ErrUnverified, r.IdempotencyKey)
	}
	if r.ProviderRef != "" && token != r.ProviderRef {
		return payments.StatusResult{}, fmt.Errorf("%w: the return names checkout form %s, the attempt started %s", payments.ErrUnverified, token, r.ProviderRef)
	}
	out, err := c.do(ctx, retrieveCheckoutFormPath, map[string]any{"locale": "en", "conversationId": r.IdempotencyKey, "token": token})
	if err != nil {
		return payments.StatusResult{}, err
	}
	if !out.ok() {
		return declineOrError(c.classify(out))
	}
	if out.BasketID != invoice || out.Token != "" && out.Token != token {
		return payments.StatusResult{}, fmt.Errorf("%w: the checkout form %s is not the payment of conversation %q", payments.ErrUnverified, token, r.IdempotencyKey)
	}
	if err := checkPrice(out, r); err != nil {
		return payments.StatusResult{}, err
	}
	return out.state()
}

// checkPrice refuses an answer that says the payment is of another amount or currency than the attempt: the invoice would be settled by a payment that does not pay
// it. An answer that names no price is not refused (UNVERIFIED which answers carry it; the checkout form and auth answers list price and currency).
func checkPrice(out response, r payments.ReturnRequest) error {
	if out.Currency != "" && r.Currency != "" && !strings.EqualFold(out.Currency, r.Currency) {
		return fmt.Errorf("%w: the payment is in %s, the attempt in %s", payments.ErrUnverified, out.Currency, r.Currency)
	}
	if out.Price == "" || r.AmountMinor <= 0 {
		return nil
	}
	cur, ok := money.LookupCurrency(r.Currency)
	if !ok {
		return fmt.Errorf("iyzico: unsupported currency %q", r.Currency)
	}
	got, exact := parseMinor(string(out.Price), cur.Exponent)
	if !exact {
		return fmt.Errorf("%w: the payment's price %q cannot be read", payments.ErrUnverified, out.Price)
	}
	if got != r.AmountMinor {
		return fmt.Errorf("%w: the payment is of %s, the attempt of %d minor units", payments.ErrUnverified, out.Price, r.AmountMinor)
	}
	return nil
}

// parseMinor reads iyzico's decimal ("3.20000000") as minor units of a currency with exp decimals. It is exact or it says no: a price with more decimals than the
// currency has is not read.
func parseMinor(s string, exp int) (int64, bool) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	frac = strings.TrimRight(frac, "0")
	if whole == "" || len(frac) > exp {
		return 0, false
	}
	n, err := strconv.ParseInt(whole+frac+strings.Repeat("0", exp-len(frac)), 10, 64)
	return n, err == nil
}
