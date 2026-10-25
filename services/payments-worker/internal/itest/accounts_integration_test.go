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

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/pkg/payments/iyzico"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/secretbox"
	"github.com/0furkancolak/quotify/pkg/testkit"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/handler"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/webhook"
	"github.com/0furkancolak/quotify/services/payments-worker/migrations"
)

// fakeIyzico answers the three iyzico endpoints the adapter calls and counts them.
type fakeIyzico struct {
	mu     sync.Mutex
	srv    *httptest.Server
	calls  map[string]int
	detail string // answer of /payment/detail
	auth   string // answer of /payment/auth
	status int    // HTTP status of /payment/detail
}

func newFakeIyzico(t *testing.T) *fakeIyzico {
	f := &fakeIyzico{calls: map[string]int{}, status: 200,
		detail: `{"status":"success","paymentId":"P1","paymentStatus":"SUCCESS"}`, auth: `{"status":"success","paymentId":"P1"}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls[r.URL.Path]++
		switch r.URL.Path {
		case "/payment/auth":
			_, _ = io.WriteString(w, f.auth)
		case "/payment/detail":
			w.WriteHeader(f.status)
			_, _ = io.WriteString(w, f.detail)
		case "/v2/payment/refund":
			_, _ = io.WriteString(w, `{"status":"success","paymentId":"P1","refundHostReference":"R1"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIyzico) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func liveCharge(tenant, invoice uuid.UUID, command uuid.UUID, currency string) *commandsv1.CommandEnvelope {
	return &commandsv1.CommandEnvelope{CommandId: command.String(), SagaId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
		Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: invoice.String(), AmountMinor: 1116,
			Currency: currency, PaymentMethod: "cardUser:cardToken", IdempotencyKey: invoice.String() + ":charge:0"}}}
}

func TestProviderAccountsLedgerAndWebhooks(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.CreateServiceDB(t, pg, "payments")
	if err := pgxx.Migrate(context.Background(), db.Migrator, migrations.FS, nil); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	fake := newFakeIyzico(t)

	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	attempts := store.Attempts{DB: pool}
	ledger := store.Ledger{DB: pool}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Attempts: attempts, Ledger: ledger, Resolver: resolver, Log: log}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: attempts,
		Ledger: ledger, Log: log, Token: "internal"}).Handler())
	defer api.Close()

	tenant := uuid.New()
	do := func(method, path, token, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("X-Internal-Token", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	settings := `{"api_key":"api","secret_key":"TOP-SECRET-KEY","base_url":"` + fake.srv.URL + `","buyer":{"name":"Ada","surname":"L","identity_number":"11111111111","address":"x","city":"Istanbul","country":"Turkey","zip_code":"34000","ip":"1.2.3.4","email":"b@example.com"}}`
	accountPath := "/admin/tenants/" + tenant.String() + "/provider-accounts/iyzico"

	t.Run("the operator endpoints need the internal token", func(t *testing.T) {
		for _, tok := range []string{"", "wrong"} {
			if code, _ := do("GET", "/admin/tenants/"+tenant.String()+"/provider-accounts", tok, ""); code != 401 {
				t.Errorf("token %q: status %d, want 401", tok, code)
			}
		}
	})

	t.Run("accounts are validated, sealed and never echoed", func(t *testing.T) {
		if code, _ := do("PUT", "/admin/tenants/"+tenant.String()+"/provider-accounts/nope?environment=live", "internal", settings); code != 404 {
			t.Errorf("unknown provider: %d", code)
		}
		if code, _ := do("PUT", accountPath, "internal", settings); code != 400 {
			t.Errorf("missing environment: %d", code)
		}
		if code, _ := do("PUT", accountPath+"?environment=live", "internal", `{"api_key":"only"}`); code != 400 {
			t.Errorf("settings without a secret key: %d", code)
		}
		if code, body := do("PUT", accountPath+"?environment=live&default=true", "internal", settings); code != 200 || strings.Contains(body, "TOP-SECRET-KEY") {
			t.Fatalf("put: %d %s", code, body)
		}
		code, body := do("GET", "/admin/tenants/"+tenant.String()+"/provider-accounts", "internal", "")
		if code != 200 || strings.Contains(body, "TOP-SECRET-KEY") || !strings.Contains(body, `"is_default":true`) || !strings.Contains(body, `"provider":"iyzico"`) {
			t.Fatalf("list: %d %s", code, body)
		}
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT settings_enc FROM provider_accounts WHERE tenant_id = $1`, tenant).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("TOP-SECRET-KEY")) {
			t.Fatal("the secret key is stored in the clear")
		}
		// A copy of the sealed settings in another tenant's row does not open.
		other := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO provider_accounts (tenant_id, provider, environment, settings_enc) VALUES ($1,'iyzico','live',$2)`, other, raw); err != nil {
			t.Fatal(err)
		}
		if _, _, err := accounts.Settings(ctx, other, "iyzico", "live"); err == nil {
			t.Error("sealed settings opened for another tenant")
		}
	})

	t.Run("go-live: a working account connected in live mode, and nothing else, makes a tenant ready", func(t *testing.T) {
		ready := func(tn uuid.UUID) bool {
			t.Helper()
			code, body := do("GET", "/admin/tenants/"+tn.String()+"/go-live", "internal", "")
			if code != 200 {
				t.Fatalf("go-live: %d %s", code, body)
			}
			var out struct {
				Ready     bool     `json:"ready"`
				Providers []string `json:"providers"`
			}
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatal(err)
			}
			if out.Ready != (len(out.Providers) > 0) {
				t.Fatalf("ready %v with providers %v", out.Ready, out.Providers)
			}
			return out.Ready
		}
		if code, _ := do("GET", "/admin/tenants/"+tenant.String()+"/go-live", "", ""); code != 401 {
			t.Errorf("without the internal token: %d", code)
		}
		if !ready(tenant) {
			t.Error("a tenant with a working live account is not ready")
		}
		if ready(uuid.New()) {
			t.Error("a tenant with no account is ready")
		}
		sandboxOnly := uuid.New()
		if code, body := do("PUT", "/admin/tenants/"+sandboxOnly.String()+"/provider-accounts/iyzico?environment=sandbox", "internal", settings); code != 200 {
			t.Fatalf("sandbox account: %d %s", code, body)
		}
		if ready(sandboxOnly) {
			t.Error("a sandbox-mode account made a tenant ready for live")
		}
		// A live record that no longer opens (here: sealed for another tenant) is not a working provider.
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT settings_enc FROM provider_accounts WHERE tenant_id = $1 AND environment = 'live'`, tenant).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		broken := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO provider_accounts (tenant_id, provider, environment, settings_enc) VALUES ($1,'iyzico','live',$2)`, broken, raw); err != nil {
			t.Fatal(err)
		}
		if ready(broken) {
			t.Error("a live account whose settings do not open made a tenant ready")
		}
	})

	invoice, command := uuid.New(), uuid.New()
	key := invoice.String() + ":charge:0"

	t.Run("a live charge goes to the tenant's default account and is written to the ledger", func(t *testing.T) {
		r, err := h.Handle(ctx, liveCharge(tenant, invoice, command, "TRY"))
		if err != nil || r.GetPaymentSucceeded().GetPspChargeId() != "P1" {
			t.Fatalf("charge = %v, %v", r, err)
		}
		if fake.count("/payment/auth") != 1 {
			t.Fatalf("auth calls = %d", fake.count("/payment/auth"))
		}
		rows, _ := ledger.List(ctx, tenant, 10)
		if len(rows) != 1 || rows[0].Kind != "charge" || rows[0].Status != "succeeded" || rows[0].AmountMinor != 1116 || rows[0].Currency != "TRY" || *rows[0].ProviderRef != "P1" || rows[0].Source != "command" {
			t.Fatalf("ledger = %+v", rows)
		}
	})

	t.Run("a redelivered charge asks iyzico first and does not charge twice", func(t *testing.T) {
		// The same command again: the attempt row exists, so the worker may have crashed after calling the provider.
		r, err := h.Handle(ctx, liveCharge(tenant, invoice, command, "TRY"))
		if err != nil || r.GetPaymentSucceeded().GetPspChargeId() != "P1" {
			t.Fatalf("redelivered charge = %v, %v", r, err)
		}
		if fake.count("/payment/auth") != 1 {
			t.Fatalf("iyzico was charged %d times", fake.count("/payment/auth"))
		}
		if fake.count("/payment/detail") != 1 {
			t.Fatalf("status lookups = %d, want 1", fake.count("/payment/detail"))
		}
	})

	t.Run("a redelivered charge whose status cannot be told is retried later, never charged", func(t *testing.T) {
		inv2, cmd2 := uuid.New(), uuid.New()
		if _, err := attempts.Start(ctx, cmd2, tenant, inv2, "charge", "live", "iyzico", inv2.String()+":charge:0", map[string]any{}); err != nil {
			t.Fatal(err)
		}
		fake.mu.Lock()
		fake.detail = `{"status":"failure","errorCode":"999","errorMessage":"unknown"}`
		fake.mu.Unlock()
		before := fake.count("/payment/auth")
		if r, err := h.Handle(ctx, liveCharge(tenant, inv2, cmd2, "TRY")); err == nil {
			t.Fatalf("got a reply %v; the command must be retried", r)
		}
		if fake.count("/payment/auth") != before {
			t.Fatal("iyzico was charged although the earlier attempt's outcome was unknown")
		}
		fake.mu.Lock()
		fake.detail = `{"status":"success","paymentId":"P1","paymentStatus":"SUCCESS"}`
		fake.mu.Unlock()
	})

	t.Run("a currency the provider cannot charge is refused before any call", func(t *testing.T) {
		before := fake.count("/payment/auth")
		r, err := h.Handle(ctx, liveCharge(tenant, uuid.New(), uuid.New(), "KWD"))
		if err != nil || r.GetPaymentErrored().GetCode() != handler.CodeCurrency || r.GetPaymentErrored().GetRetryable() {
			t.Fatalf("reply = %v, %v", r, err)
		}
		if fake.count("/payment/auth") != before {
			t.Error("iyzico was called")
		}
	})

	t.Run("a refund follows the provider of the original charge", func(t *testing.T) {
		cmd := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{InvoiceId: invoice.String(), PspChargeId: "P1",
				AmountMinor: 1116, Currency: "TRY", IdempotencyKey: invoice.String() + ":refund"}}}
		r, err := h.Handle(ctx, cmd)
		if err != nil || r.GetPaymentRefunded().GetPspRefundId() != "R1" {
			t.Fatalf("refund = %v, %v", r, err)
		}
		rows, _ := ledger.List(ctx, tenant, 10)
		var refunds int
		for _, row := range rows {
			if row.Kind == "refund" && row.Status == "succeeded" && *row.ProviderRef == "R1" {
				refunds++
			}
		}
		if refunds != 1 {
			t.Fatalf("ledger = %+v", rows)
		}
	})

	notify := func(ref, conversation string) (int, string) {
		b, _ := json.Marshal(map[string]any{"iyziEventType": "API_AUTH", "iyziReferenceCode": ref, "paymentId": 1, "paymentConversationId": conversation, "status": "SUCCESS", "iyziEventTime": 1})
		return do("POST", "/webhooks/iyzico", "", string(b))
	}

	t.Run("a webhook is confirmed with iyzico and recorded once", func(t *testing.T) {
		code, body := notify("ev-1", key)
		if code != 200 || !strings.Contains(body, "processed") {
			t.Fatalf("webhook: %d %s", code, body)
		}
		if code, body = notify("ev-1", key); code != 200 || !strings.Contains(body, "duplicate") {
			t.Fatalf("the same event again: %d %s", code, body)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_ledger WHERE tenant_id = $1 AND kind = 'charge' AND provider_ref = 'P1'`, tenant).Scan(&n); err != nil || n != 1 {
			t.Fatalf("charge rows for P1 = %d, %v; the command's answer and the webhook are one fact", n, err)
		}
	})

	t.Run("a notification about a charge we never made is ignored", func(t *testing.T) {
		before := fake.count("/payment/detail")
		code, body := notify("ev-unknown", "someone-elses-charge")
		if code != 200 || !strings.Contains(body, "ignored") {
			t.Fatalf("%d %s", code, body)
		}
		if fake.count("/payment/detail") != before {
			t.Error("iyzico was asked about a charge that is not ours")
		}
	})

	t.Run("a notification that disagrees with iyzico is overruled and the discrepancy is visible", func(t *testing.T) {
		fake.mu.Lock()
		fake.detail = `{"status":"success","paymentId":"P1","paymentStatus":"FAILURE"}`
		fake.mu.Unlock()
		if code, body := notify("ev-2", key); code != 200 || !strings.Contains(body, "processed") {
			t.Fatalf("%d %s", code, body)
		}
		n, err := ledger.Outcomes(ctx, "iyzico", "charge", "P1")
		if err != nil || n != 2 {
			t.Fatalf("outcomes for P1 = %d, %v; the command said succeeded and iyzico now says declined", n, err)
		}
		fake.mu.Lock()
		fake.detail = `{"status":"success","paymentId":"P1","paymentStatus":"SUCCESS"}`
		fake.mu.Unlock()
	})

	t.Run("when iyzico cannot confirm, the webhook is retried, not lost", func(t *testing.T) {
		fake.mu.Lock()
		fake.status = 503
		fake.detail = `{}`
		fake.mu.Unlock()
		if code, _ := notify("ev-3", key); code != 503 {
			t.Fatalf("status %d, want 503 so iyzico retries", code)
		}
		fake.mu.Lock()
		fake.status = 200
		fake.detail = `{"status":"success","paymentId":"P1","paymentStatus":"SUCCESS"}`
		fake.mu.Unlock()
		if code, body := notify("ev-3", key); code != 200 || !strings.Contains(body, "processed") {
			t.Fatalf("the retry: %d %s", code, body)
		}
	})

	t.Run("bad requests", func(t *testing.T) {
		if code, _ := do("POST", "/webhooks/unknown", "", `{}`); code != 404 {
			t.Errorf("unknown provider: %d", code)
		}
		if code, _ := do("POST", "/webhooks/iyzico", "", `not json`); code != 400 {
			t.Errorf("garbage: %d", code)
		}
	})

	t.Run("deleting an account sends live charges back to the configured provider", func(t *testing.T) {
		if code, _ := do("DELETE", accountPath+"?environment=live", "internal", ""); code != 204 {
			t.Fatalf("delete: %d", code)
		}
		r, err := h.Handle(ctx, liveCharge(tenant, uuid.New(), uuid.New(), "TRY"))
		if err != nil || r.GetPaymentErrored().GetCode() != handler.CodeNotConfigured {
			t.Fatalf("reply = %v, %v; with no account and no live provider the charge is not configured", r, err)
		}
	})
}
