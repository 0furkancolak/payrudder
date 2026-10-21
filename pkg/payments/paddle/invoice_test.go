package paddle

import (
	"context"
	"testing"

	"github.com/0furkancolak/quotify/pkg/payments"
)

func TestCreateInvoiceCheckout(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func() (int, string) {
		return 200, `{"data":{"id":"txn_01","checkout":{"url":"https://pay.example/txn_01"}}}`
	}
	var _ payments.InvoiceCheckoutProvider = c
	co, err := c.CreateInvoiceCheckout(context.Background(), payments.CheckoutInvoiceRequest{IdempotencyKey: "link:inv-1:1", InvoiceID: "inv-1", Number: "INV-1",
		Description: "Consulting", AmountMinor: 12345, Currency: "USD"})
	if err != nil || co.URL != "https://pay.example/txn_01" || co.Ref != "txn_01" {
		t.Fatalf("checkout = %+v, %v", co, err)
	}
	if f.method != "POST" || f.path != "/transactions" {
		t.Errorf("request %s %s", f.method, f.path)
	}
	cd := f.body["custom_data"].(map[string]any)
	if cd["quotify_key"] != "link:inv-1:1" {
		t.Errorf("custom_data = %v", cd)
	}
	item := f.body["items"].([]any)[0].(map[string]any)
	pr := item["price"].(map[string]any)["unit_price"].(map[string]any)
	if pr["amount"] != "12345" || pr["currency_code"] != "USD" || item["quantity"] != float64(1) {
		t.Errorf("item = %v", item)
	}
}

func TestCreateInvoiceCheckoutNeedsAKeyAndAUrl(t *testing.T) {
	f, c := newFake(t, nil)
	if _, err := c.CreateInvoiceCheckout(context.Background(), payments.CheckoutInvoiceRequest{AmountMinor: 1, Currency: "USD"}); err == nil {
		t.Error("a key is required")
	}
	f.reply = func() (int, string) { return 200, `{"data":{"id":"txn_01"}}` }
	if _, err := c.CreateInvoiceCheckout(context.Background(), payments.CheckoutInvoiceRequest{IdempotencyKey: "k", AmountMinor: 1, Currency: "USD"}); err == nil {
		t.Error("a transaction without a checkout url must fail")
	}
}
