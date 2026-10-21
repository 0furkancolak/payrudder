package iyzico

import (
	"context"
	"testing"

	"github.com/0furkancolak/quotify/pkg/payments"
)

func TestCreateInvoiceCheckout(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","token":"tok-1","paymentPageUrl":"https://sandbox.iyzico.example/pay/tok-1"}`
	}
	var _ payments.InvoiceCheckoutProvider = c
	co, err := c.CreateInvoiceCheckout(context.Background(), payments.CheckoutInvoiceRequest{IdempotencyKey: "link:inv-1:1", InvoiceID: "inv-1", Number: "INV-2026-000001",
		AmountMinor: 1116, Currency: "TRY", CustomerName: "Grace Hopper", CustomerEmail: "g@example.com", ReturnURL: "https://q.example/i/tok"})
	if err != nil || co.URL != "https://sandbox.iyzico.example/pay/tok-1" || co.Ref != "tok-1" {
		t.Fatalf("checkout = %+v, %v", co, err)
	}
	b := f.last
	if f.paths[0] != "/payment/iyzipos/checkoutform/initialize/auth/ecom" || b["conversationId"] != "link:inv-1:1" || b["price"] != "11.16" || b["callbackUrl"] != "https://q.example/i/tok" {
		t.Errorf("path %v request %v", f.paths, b)
	}
	buyer := b["buyer"].(map[string]any)
	if buyer["name"] != "Grace" || buyer["surname"] != "Hopper" || buyer["email"] != "g@example.com" {
		t.Errorf("buyer = %v", buyer)
	}
	if !Caps().HostedCheckout {
		t.Error("iyzico hosts a checkout form")
	}
}

func TestCreateInvoiceCheckoutRefused(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"failure","errorCode":"1000","errorGroup":"INVALID"}`
	}
	_, err := c.CreateInvoiceCheckout(context.Background(), payments.CheckoutInvoiceRequest{IdempotencyKey: "k", AmountMinor: 100, Currency: "TRY", ReturnURL: "https://q.example"})
	if err == nil {
		t.Fatal("a refused form must fail")
	}
	if _, err := c.CreateInvoiceCheckout(context.Background(), payments.CheckoutInvoiceRequest{IdempotencyKey: "k", AmountMinor: 100, Currency: "TRY"}); err == nil {
		t.Error("a return url is required")
	}
}
