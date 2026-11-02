//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// causeBilling is billing-svc as far as a settle goes: the first payment for an invoice settles it, and a later call is a repeat only when it names the very provider and
// reference that settled it (billing keeps the cause "settle:<provider>:<reference>" in the saga's events). A call that names the payment in another way is a second
// payment, which is what makes the worker raise its alert.
type causeBilling struct {
	recBilling
	mu    sync.Mutex
	cause map[string]string // invoice -> the cause that settled it
	calls []string          // the provider of every settle call
}

func (b *causeBilling) SettleInvoicePayment(_ context.Context, _ uuid.UUID, req *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, req.GetProvider())
	cause := req.GetProvider() + ":" + req.GetProviderRef()
	if first, ok := b.cause[req.GetInvoiceId()]; ok {
		return &billingv1.SettleInvoicePaymentResponse{Settled: false, Repeat: first == cause}, nil
	}
	b.cause[req.GetInvoiceId()] = cause
	return &billingv1.SettleInvoicePaymentResponse{Settled: true}, nil
}

// A tenant can connect several accounts at one provider, and the attempt of a hosted payment is recorded under the account it was made through (iyzico~tr). The two ways a
// payment comes back, the payer's return and the provider's notification, must both be that account's: the same ledger row, the same name to billing (so the second is a
// repeat, not a second payment), and the account that confirms the notification is the attempt's, whichever URL the provider was told to post to. Before, the return wrote
// under "iyzico", a notification to a labelled URL under "iyzico~tr" (two rows, "paid twice"), and a notification to the plain URL found no account named "iyzico" and was
// retried for ever.
func TestAPaymentOnALabelledAccountIsOnePaymentWhicheverWayItComesBack(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	pool := cdb.DB
	ctx := context.Background()

	iyz := &hostedIyzico{bodies: map[string][]map[string]any{}, paid: map[string]bool{}}
	psp := httptest.NewServer(iyz)
	defer psp.Close()
	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	ledger := store.Ledger{DB: pool}
	billing := &causeBilling{recBilling: recBilling{}, cause: map[string]string{}}
	plans := &recPlans{customers: map[string]*plansv1.Customer{"ext-1": {Id: "cus-1", ExternalId: "ext-1", Name: "Ada Lovelace", Email: "ada@example.com", Phone: "+905321234567",
		TaxId: "11111111110", Address: &plansv1.Address{Line1: "Nisantasi 1", City: "Istanbul", PostalCode: "34000", Country: "TR"}}}}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: store.Attempts{DB: pool}, Ledger: ledger,
		Subscriptions: store.Subscriptions{DB: pool}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Token: "internal", Plans: plans, Billing: billing}).Handler())
	defer api.Close()

	// Each scenario has a tenant of its own, whose only iyzico account is a labelled one: there is no account called "iyzico". (The fake answers one payment id for every
	// checkout, and the ledger keeps a payment once per tenant.)
	var tenant uuid.UUID
	call := func(path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest("POST", api.URL+path, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	// pay opens the checkout of a fresh invoice and has the payer pay on iyzico's page; it returns what the notification will name.
	pay := func(t *testing.T) (invoice uuid.UUID, key, token string) {
		t.Helper()
		tenant = uuid.New()
		if err := accounts.Put(ctx, tenant, "iyzico~tr", "live", []byte(`{"api_key":"api","secret_key":"sec","base_url":"`+psp.URL+`"}`), true); err != nil {
			t.Fatal(err)
		}
		billing.mu.Lock()
		billing.calls = nil
		billing.mu.Unlock()
		invoice = uuid.New()
		code, out := call("/admin/tenants/"+tenant.String()+"/invoice-payments", `{"invoice_id":"`+invoice.String()+`","saga_id":"`+uuid.NewString()+`","number":"INV-1","amount_minor":12345,`+
			`"currency":"TRY","method":"checkout","country":"TR","customer_external_id":"ext-1","payer_ip":"85.34.78.112","customer_email":"ada@example.com","customer_name":"Ada Lovelace",`+
			`"return_url":"https://q.example/i/tok","callback_url":"https://q.example/i/tok/return/{key}","environment":"live"}`)
		if code != 201 {
			t.Fatalf("checkout = %d %v", code, out)
		}
		key, token = "link:"+invoice.String()+":1", "tok-"+invoice.String()[:4]
		iyz.mu.Lock()
		iyz.paid[token] = true
		iyz.mu.Unlock()
		return invoice, key, token
	}
	notify := func(url, key, token string) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"iyziEventType": "CHECKOUT_FORM_AUTH", "iyziEventTime": 1, "iyziReferenceCode": "ev-" + uuid.NewString(), "iyziPaymentId": 77, "token": token,
			"paymentConversationId": key, "status": "SUCCESS"})
		return call(url, string(b))
	}
	rows := func(invoice uuid.UUID) (n int, providers []string) {
		t.Helper()
		all, err := ledger.LookupInvoices(ctx, tenant, []uuid.UUID{invoice})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range all {
			if r.Kind == "charge" && r.Status == "succeeded" {
				n++
				providers = append(providers, r.Provider)
			}
		}
		return n, providers
	}

	for _, tc := range []struct{ name, url string }{
		{"a notification to the plain URL", "/webhooks/iyzico"},
		{"a notification to the URL of the labelled account", "/webhooks/iyzico~tr"},
	} {
		t.Run("the return, then "+tc.name, func(t *testing.T) {
			invoice, key, token := pay(t)
			alerts := counted(t, "payment_discrepancies")
			if code, res := call("/admin/invoice-payments/return", `{"key":"`+key+`","form":{"token":["`+token+`"]}}`); code != 200 || res["state"] != "paid" {
				t.Fatalf("return = %d %v", code, res)
			}
			if code, out := notify(tc.url, key, token); code != 200 || out["status"] != "processed" {
				t.Fatalf("notification = %d %v; want it processed (the attempt's account confirms it)", code, out)
			}
			if n, providers := rows(invoice); n != 1 || providers[0] != "iyzico~tr" {
				t.Errorf("succeeded charges of the invoice: %d under %v; want one, under the account's key iyzico~tr", n, providers)
			}
			billing.mu.Lock()
			calls := append([]string(nil), billing.calls...)
			billing.mu.Unlock()
			for _, p := range calls {
				if p != "iyzico~tr" {
					t.Errorf("billing was told the provider %q; want the account's key iyzico~tr every time", p)
				}
			}
			if got := counted(t, "payment_discrepancies"); got != alerts {
				t.Errorf("payment_discrepancies went from %d to %d: the same payment was taken for a second one", alerts, got)
			}
		})
		t.Run("the notification first, then the return, "+tc.name, func(t *testing.T) {
			invoice, key, token := pay(t)
			alerts := counted(t, "payment_discrepancies", attribute.String("provider", payments.BaseProvider(tc.url[len("/webhooks/"):])))
			if code, out := notify(tc.url, key, token); code != 200 || out["status"] != "processed" {
				t.Fatalf("notification = %d %v; want it processed", code, out)
			}
			if code, res := call("/admin/invoice-payments/return", `{"key":"`+key+`","form":{"token":["`+token+`"]}}`); code != 200 || res["state"] != "paid" {
				t.Fatalf("return = %d %v", code, res)
			}
			if n, providers := rows(invoice); n != 1 || providers[0] != "iyzico~tr" {
				t.Errorf("succeeded charges of the invoice: %d under %v; want one, under iyzico~tr", n, providers)
			}
			if got := counted(t, "payment_discrepancies", attribute.String("provider", payments.BaseProvider(tc.url[len("/webhooks/"):]))); got != alerts {
				t.Errorf("payment_discrepancies went from %d to %d: the return was taken for a second payment", alerts, got)
			}
		})
	}
}
