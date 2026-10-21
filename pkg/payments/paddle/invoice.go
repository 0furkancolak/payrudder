package paddle

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/0furkancolak/quotify/pkg/payments"
)

// CreateInvoiceCheckout starts a hosted checkout for one invoice: a transaction with a non-catalogue item for the invoice total, tagged with the key
// of the charge (custom_data.quotify_key), which ConfirmNotification reads back from transaction.completed.
// UNVERIFIED: that Paddle accepts a non-catalogue price object in exactly this shape; the reference describes it, no sandbox has run it.
func (c *Client) CreateInvoiceCheckout(ctx context.Context, r payments.CheckoutInvoiceRequest) (payments.Checkout, error) {
	if r.IdempotencyKey == "" || r.AmountMinor <= 0 || r.Currency == "" {
		return payments.Checkout{}, errors.New("paddle: an invoice checkout needs a key, an amount and a currency")
	}
	name := "Invoice"
	if r.Number != "" {
		name += " " + r.Number
	}
	desc := r.Description
	if desc == "" {
		desc = name
	}
	req := map[string]any{
		"items": []map[string]any{{
			"quantity": 1,
			"price": map[string]any{
				"description": desc, "name": name,
				"unit_price": map[string]string{"amount": strconv.FormatInt(r.AmountMinor, 10), "currency_code": r.Currency},
				"product":    map[string]string{"name": name, "tax_category": "standard"},
			},
		}},
		"collection_mode": "automatic",
		"custom_data":     map[string]string{"quotify_key": r.IdempotencyKey, "quotify_invoice_id": r.InvoiceID},
	}
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Checkout *struct {
				URL string `json:"url"`
			} `json:"checkout"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/transactions", req, &out); err != nil {
		return payments.Checkout{}, err
	}
	if out.Data.Checkout == nil || out.Data.Checkout.URL == "" {
		return payments.Checkout{}, errors.New("paddle: the transaction has no checkout url (is a default payment link set in Paddle?)")
	}
	return payments.Checkout{URL: out.Data.Checkout.URL, Ref: out.Data.ID}, nil
}
