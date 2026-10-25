//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/paddle"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

// PreviewRouting makes recPlans a webhook.PlansAPI: it sends everyone to the provider in routeTo.
func (r *recPlans) PreviewRouting(context.Context, uuid.UUID, string, string) (string, error) {
	return "", nil
}

// SettleInvoicePayment keeps the reconciliation's fake a webhook.BillingAPI.
func (fakeBilling) SettleInvoicePayment(context.Context, uuid.UUID, *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error) {
	return &billingv1.SettleInvoicePaymentResponse{}, nil
}

// fakeBank is a provider the customer pays by hand: what a bank-transfer provider will be.
type fakeBank struct{}

func (fakeBank) Name() string { return "fakebank" }
func (fakeBank) Charge(context.Context, payments.ChargeRequest) (payments.ChargeResult, error) {
	return payments.ChargeResult{}, payments.ErrUnsupported
}
func (fakeBank) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	return payments.RefundResult{}, payments.ErrUnsupported
}
func (fakeBank) Status(context.Context, string) (payments.StatusResult, error) {
	return payments.StatusResult{State: payments.StateNotFound}, nil
}
func (fakeBank) Capabilities() payments.Capabilities { return payments.Capabilities{Name: "fakebank"} }
func (fakeBank) PaymentInstructions(_ context.Context, r payments.InstructionsRequest) (payments.Instructions, error) {
	return payments.Instructions{Reference: "REF-" + r.Number, Lines: []payments.InstructionLine{{Label: "IBAN", Value: "DE00 1234"}}}, nil
}

// recBilling stands in for billing-svc: it records the settle calls.
type recBilling struct {
	mu      sync.Mutex
	settled []*billingv1.SettleInvoicePaymentRequest
	err     error
	answer  bool
}

func (b *recBilling) ListInvoices(context.Context, uuid.UUID, *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	return &billingv1.ListInvoicesResponse{}, nil
}
func (b *recBilling) GetInvoice(context.Context, uuid.UUID, string) (*billingv1.Invoice, error) {
	return &billingv1.Invoice{}, nil
}
func (b *recBilling) SettleInvoicePayment(_ context.Context, _ uuid.UUID, req *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return nil, b.err
	}
	b.settled = append(b.settled, req)
	return &billingv1.SettleInvoicePaymentResponse{Settled: b.answer}, nil
}
func (b *recBilling) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.settled)
}

func TestHostedPaymentsThroughTheWorker(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "payments", migrations.FS)
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()

	var transactions atomic.Int32
	psp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/transactions" {
			w.WriteHeader(404)
			return
		}
		n := transactions.Add(1)
		_, _ = io.WriteString(w, `{"data":{"id":"txn_0`+string(rune('0'+n))+`","checkout":{"url":"https://pay.example/txn_0`+string(rune('0'+n))+`"}}}`)
	}))
	defer psp.Close()

	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	registry := payments.NewRegistry()
	paddle.Register(registry)
	registry.Register("fakebank", func([]byte) (payments.Provider, error) { return fakeBank{}, nil })
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	billing := &recBilling{answer: true}
	ledger := store.Ledger{DB: pool}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: store.Attempts{DB: pool},
		Ledger: ledger, Subscriptions: store.Subscriptions{DB: pool}, Log: log, Token: "internal", Plans: &recPlans{}, Billing: billing}).Handler())
	defer api.Close()

	tenant, stranger := uuid.New(), uuid.New()
	const secret = "pdl_whsec"
	if err := accounts.Put(ctx, tenant, "paddle", "live", []byte(`{"api_key":"pdl_secret","base_url":"`+psp.URL+`","webhook_secret":"`+secret+`","usage_product_id":"pro_usage"}`), true); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Put(ctx, tenant, "fakebank", "live", []byte(`{}`), false); err != nil {
		t.Fatal(err)
	}

	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
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
	hook := func(event string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", api.URL+"/webhooks/paddle/"+tenant.String()+"/live", strings.NewReader(event))
		req.Header.Set("Paddle-Signature", paddle.SignatureHeader(secret, time.Now(), []byte(event)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	txEvent := func(id, typ, txn, key string) string {
		return `{"event_id":"` + id + `","event_type":"` + typ + `","occurred_at":"` + time.Now().UTC().Format(time.RFC3339) + `","data":{"id":"` + txn +
			`","status":"completed","currency_code":"USD","custom_data":{"payrudder_key":"` + key + `"},"details":{"totals":{"grand_total":"12345"}}}}`
	}
	base := "/admin/tenants/" + tenant.String()
	invoice := uuid.New()
	pay := `{"invoice_id":"` + invoice.String() + `","saga_id":"` + uuid.NewString() + `","number":"INV-1","amount_minor":12345,"currency":"USD","method":"checkout","country":"DE",` +
		`"return_url":"https://q.example/i/tok","environment":"live"}`
	key1 := "link:" + invoice.String() + ":1"

	t.Run("the methods come from the tenant's accounts: a checkout and every manual provider", func(t *testing.T) {
		code, out := call("GET", base+"/invoice-methods?environment=live&currency=USD&country=DE", "")
		if code != 200 {
			t.Fatalf("methods = %d %v", code, out)
		}
		var ids []string
		for _, m := range out["methods"].([]any) {
			ids = append(ids, m.(map[string]any)["id"].(string))
		}
		if strings.Join(ids, ",") != "checkout,instructions:fakebank" {
			t.Errorf("methods = %v", ids)
		}
		// A currency the checkout provider cannot take leaves only what can be paid by hand (fakebank declares no currency limit).
		_, out = call("GET", base+"/invoice-methods?environment=live&currency=TRY", "")
		if len(out["methods"].([]any)) != 1 {
			t.Errorf("TRY methods = %v", out)
		}
		// A sandbox invoice never reaches a provider.
		_, out = call("GET", base+"/invoice-methods?environment=sandbox&currency=USD", "")
		if len(out["methods"].([]any)) != 0 {
			t.Errorf("sandbox methods = %v", out)
		}
		if code, _ := call("POST", base+"/invoice-payments", strings.Replace(pay, `"live"`, `"sandbox"`, 1)); code != 409 {
			t.Errorf("a sandbox payment = %d, want 409", code)
		}
	})

	t.Run("a checkout is opened once, handed out again while open, and leaves a pending row", func(t *testing.T) {
		code, out := call("POST", base+"/invoice-payments", pay)
		if code != 201 || out["kind"] != "redirect" || out["url"] != "https://pay.example/txn_01" {
			t.Fatalf("checkout = %d %v", code, out)
		}
		code, again := call("POST", base+"/invoice-payments", pay)
		if code != 200 || again["url"] != out["url"] || transactions.Load() != 1 {
			t.Fatalf("second checkout = %d %v after %d transaction(s); a double click must meet the same page", code, again, transactions.Load())
		}
		rows, err := ledger.List(ctx, tenant, 10)
		if err != nil || len(rows) != 1 || rows[0].Status != "pending" || rows[0].Kind != "charge" || rows[0].Provider != "paddle" {
			t.Fatalf("ledger = %+v, %v", rows, err)
		}
	})

	t.Run("the provider's confirmation, not the browser, settles the invoice", func(t *testing.T) {
		if billing.count() != 0 {
			t.Fatal("billing was told before any confirmation")
		}
		// Billing is down: the provider is answered 503 and will send the notification again.
		billing.err = errors.New("billing is down")
		if code := hook(txEvent("evt_1", "transaction.completed", "txn_01", key1)); code != 503 {
			t.Fatalf("hook with billing down = %d, want 503", code)
		}
		billing.err = nil
		if code := hook(txEvent("evt_1", "transaction.completed", "txn_01", key1)); code != 200 {
			t.Fatalf("hook = %d", code)
		}
		if billing.count() != 1 {
			t.Fatalf("settle calls = %d, want 1", billing.count())
		}
		s := billing.settled[0]
		if s.GetInvoiceId() != invoice.String() || s.GetAmountMinor() != 12345 || s.GetCurrency() != "USD" || s.GetProvider() != "paddle" || s.GetProviderRef() != "txn_01" {
			t.Errorf("settle = %v", s)
		}
		rows, _ := ledger.List(ctx, tenant, 10)
		var succeeded int
		for _, r := range rows {
			if r.Status == "succeeded" && r.InvoiceID != nil && *r.InvoiceID == invoice.String() {
				succeeded++
			}
		}
		if succeeded != 1 {
			t.Errorf("ledger = %+v, want one succeeded charge for the invoice", rows)
		}
		// The same notification again is a duplicate and settles nothing twice.
		if hook(txEvent("evt_1", "transaction.completed", "txn_01", key1)); billing.count() != 1 {
			t.Errorf("settle calls after a duplicate = %d", billing.count())
		}
	})

	t.Run("a declined hosted payment writes the ledger row and settles nothing", func(t *testing.T) {
		code, out := call("POST", base+"/invoice-payments", pay)
		if code != 201 || out["url"] != "https://pay.example/txn_02" {
			t.Fatalf("second attempt = %d %v; a settled checkout is not handed out again", code, out)
		}
		if hook(txEvent("evt_2", "transaction.payment_failed", "txn_02", "link:"+invoice.String()+":2")) != 200 {
			t.Fatal("hook failed")
		}
		if billing.count() != 1 {
			t.Errorf("settle calls = %d, want still 1", billing.count())
		}
		rows, _ := ledger.LookupInvoices(ctx, tenant, []uuid.UUID{invoice})
		declined := 0
		for _, r := range rows {
			if r.Status == "declined" {
				declined++
			}
		}
		if declined != 1 {
			t.Errorf("rows = %+v, want one declined", rows)
		}
	})

	t.Run("a manual provider's instructions are the same on every request and leave one pending row", func(t *testing.T) {
		other := uuid.New()
		body := strings.NewReplacer(invoice.String(), other.String(), `"checkout"`, `"instructions:fakebank"`).Replace(pay)
		code, out := call("POST", base+"/invoice-payments", body)
		if code != 200 || out["kind"] != "instructions" || out["reference"] != "REF-INV-1" || len(out["lines"].([]any)) != 1 {
			t.Fatalf("instructions = %d %v", code, out)
		}
		call("POST", base+"/invoice-payments", body)
		rows, _ := ledger.LookupInvoices(ctx, tenant, []uuid.UUID{other})
		if len(rows) != 1 || rows[0].Status != "pending" {
			t.Errorf("rows = %+v, want one pending", rows)
		}
	})

	t.Run("the ledger lookup is the tenant's own", func(t *testing.T) {
		code, out := call("POST", base+"/ledger/lookup", `{"invoice_ids":["`+invoice.String()+`"]}`)
		rows, _ := out["rows"].([]any)
		if code != 200 || len(rows) < 3 {
			t.Fatalf("lookup = %d %v", code, out)
		}
		if _, leaked := rows[0].(map[string]any)["provider_ref"]; leaked {
			t.Error("a customer-facing row carries the provider reference")
		}
		code, out = call("POST", "/admin/tenants/"+stranger.String()+"/ledger/lookup", `{"invoice_ids":["`+invoice.String()+`"]}`)
		if code != 200 || len(out["rows"].([]any)) != 0 {
			t.Errorf("another tenant's lookup = %d %v, want no rows", code, out)
		}
		if code, _ := call("POST", base+"/ledger/lookup", `{"invoice_ids":["nope"]}`); code != 400 {
			t.Errorf("a bad id = %d", code)
		}
		ids := make([]string, 201)
		for i := range ids {
			ids[i] = uuid.NewString()
		}
		b, _ := json.Marshal(map[string]any{"invoice_ids": ids})
		if code, _ := call("POST", base+"/ledger/lookup", string(b)); code != 400 {
			t.Errorf("201 ids = %d", code)
		}
		req, _ := http.NewRequest("POST", api.URL+base+"/ledger/lookup", strings.NewReader(`{}`))
		if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
			t.Errorf("without the token: %v %v", resp, err)
		}
	})
}
