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
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/payments/paddle"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts"
	accountscommand "github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts/command"
	accountsquery "github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/migrations"
)

func sandboxCharge(tenant, invoice uuid.UUID, method string) *commandsv1.CommandEnvelope {
	return &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX,
		Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: invoice.String(), AmountMinor: 1116,
			Currency: "TRY", PaymentMethod: method, IdempotencyKey: invoice.String() + ":charge:0"}}}
}

// A person's sandbox connection to a provider's test mode (phase 54): stored for the sandbox tenant, checked for its mode, read back without a secret, used by a
// sandbox charge that carries the provider's own token and by nothing else, and never by a live command.
func TestSandboxProviderAccounts(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "payments", migrations.FS)
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	fake := newFakeIyzico(t)
	mockCalls := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockCalls++
		_, _ = io.WriteString(w, `{"id":"mock_ch"}`)
	}))
	defer mock.Close()

	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	paddle.Register(registry)
	acct := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: acct}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &handler.Handler{Sandbox: payments.NewMockPSP(mock.URL, 0), Attempts: store.Attempts{DB: pool}, Ledger: store.Ledger{DB: pool}, Resolver: resolver, Log: log}

	cmdDeps := accountscommand.Deps{Registry: registry, Accounts: acct, Log: log}
	api := httptest.NewServer(mux(&accounts.Handler{Command: cmdDeps, Query: accountsquery.Deps{Registry: registry, Accounts: acct, Log: log}, Log: log}))
	defer api.Close()
	do := func(method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	tenant := uuid.New()
	base := "/admin/tenants/" + tenant.String()
	buyer := `"buyer":{"name":"Ada","surname":"L","identity_number":"11111111111","address":"x","city":"Istanbul","country":"Turkey","zip_code":"34000","ip":"1.2.3.4","email":"b@example.com"}`
	settings := `{"api_key":"sandbox-api","secret_key":"TOP-SECRET-KEY","base_url":"` + fake.srv.URL + `",` + buyer + `}`

	t.Run("a person's connection is verified against the provider's own hosts and its mode", func(t *testing.T) {
		// A host that is not one of the provider's: the secret keys would go to whoever the person names.
		if code, body := do("PUT", base+"/provider-accounts/iyzico?environment=sandbox&verify=true", settings); code != 400 || !strings.Contains(body, "payments.account.endpoint_not_allowed") {
			t.Errorf("an unknown host: %d %s", code, body)
		}
		// The live host and a live-format key are refused before the provider is asked anything.
		live := `{"api_key":"a","secret_key":"s","base_url":"` + iyzico.LiveURL + `",` + buyer + `}`
		if code, body := do("PUT", base+"/provider-accounts/iyzico?environment=sandbox&verify=true", live); code != 400 || !strings.Contains(body, "payments.account.livemode_mismatch") {
			t.Errorf("the live host in the sandbox: %d %s", code, body)
		}
		paddleLive := `{"api_key":"pdl_live_apikey_1","base_url":"` + paddle.SandboxURL + `","webhook_secret":"w","usage_product_id":"pro_1"}`
		if code, body := do("PUT", base+"/provider-accounts/paddle?environment=sandbox&verify=true", paddleLive); code != 400 || !strings.Contains(body, "payments.account.livemode_mismatch") {
			t.Errorf("a live Paddle key in the sandbox: %d %s", code, body)
		}
		// The operator's door (no verify) also refuses a live endpoint, as before.
		if code, _ := do("PUT", base+"/provider-accounts/iyzico?environment=sandbox", live); code != 400 {
			t.Errorf("the live host without verify: %d", code)
		}
		if code, body := do("GET", base+"/provider-accounts", ""); code != 200 || strings.Contains(body, `"provider":"iyzico"`) {
			t.Errorf("a refused connection was stored: %d %s", code, body)
		}
	})

	if code, body := do("PUT", base+"/provider-accounts/iyzico?environment=sandbox&default=true", settings); code != 200 || strings.Contains(body, "TOP-SECRET-KEY") {
		t.Fatalf("sandbox account: %d %s", code, body)
	}

	t.Run("the health of an environment lists only that environment's accounts, and a sandbox account does not make a tenant ready for live", func(t *testing.T) {
		code, body := do("GET", base+"/provider-health?environment=sandbox", "")
		var out accountsquery.GoLive
		if err := json.Unmarshal([]byte(body), &out); err != nil || code != 200 || len(out.Accounts) != 1 || !out.Accounts[0].Working || !out.Accounts[0].Default || strings.Contains(body, "TOP-SECRET-KEY") {
			t.Fatalf("sandbox health: %d %s", code, body)
		}
		if code, body := do("GET", base+"/provider-health?environment=live", ""); code != 200 || !strings.Contains(body, `"accounts":[]`) {
			t.Errorf("live health: %d %s", code, body)
		}
		if code, body := do("GET", base+"/go-live", ""); code != 200 || !strings.Contains(body, `"ready":false`) {
			t.Errorf("go-live: %d %s", code, body)
		}
		if code, _ := do("GET", base+"/provider-health", ""); code != 400 {
			t.Errorf("health without an environment: %d", code)
		}
	})

	t.Run("a sandbox charge with the provider's own token goes to the sandbox account, a scenario to the mock", func(t *testing.T) {
		inv := uuid.New()
		r, err := h.Handle(ctx, sandboxCharge(tenant, inv, "cardUser:cardToken"))
		if err != nil || r.GetPaymentSucceeded().GetPspChargeId() != "P1" || fake.count("/payment/auth") != 1 || mockCalls != 0 {
			t.Fatalf("provider charge = %v, %v (auth %d, mock %d)", r, err, fake.count("/payment/auth"), mockCalls)
		}
		var env, provider string
		if err := pool.QueryRow(ctx, `SELECT environment, provider FROM payment_ledger WHERE tenant_id = $1 AND invoice_id = $2`, tenant, inv).Scan(&env, &provider); err != nil || env != "sandbox" || provider != "iyzico" {
			t.Errorf("ledger row: %q %q, %v", env, provider, err)
		}
		r, err = h.Handle(ctx, sandboxCharge(tenant, uuid.New(), "pm_sandbox_success"))
		if err != nil || r.GetPaymentSucceeded().GetPspChargeId() != "mock_ch" || fake.count("/payment/auth") != 1 || mockCalls != 1 {
			t.Fatalf("scenario charge = %v, %v (auth %d, mock %d)", r, err, fake.count("/payment/auth"), mockCalls)
		}
		// A refund of the first invoice follows the provider it was charged through.
		refund := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX,
			Command: &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{InvoiceId: inv.String(), PspChargeId: "P1", AmountMinor: 1116,
				Currency: "TRY", IdempotencyKey: inv.String() + ":refund"}}}
		if r, err := h.Handle(ctx, refund); err != nil || r.GetPaymentRefunded().GetPspRefundId() != "R1" {
			t.Errorf("refund = %v, %v", r, err)
		}
	})

	t.Run("live never uses the sandbox account, and a sandbox tenant never uses a live one", func(t *testing.T) {
		before := fake.count("/payment/auth")
		live := liveCharge(tenant, uuid.New(), uuid.New(), "TRY")
		if r, err := h.Handle(ctx, live); err != nil || r.GetPaymentErrored().GetCode() != handler.CodeNotConfigured || fake.count("/payment/auth") != before {
			t.Errorf("live charge with only a sandbox account: %v, %v", r, err)
		}
		// Another tenant that has a live account only: its sandbox charge goes to the mock.
		liveOnly := uuid.New()
		liveSettings := strings.Replace(settings, "sandbox-api", "live-api", 1)
		if code, body := do("PUT", "/admin/tenants/"+liveOnly.String()+"/provider-accounts/iyzico?environment=live&default=true", liveSettings); code != 200 {
			t.Fatalf("live account: %d %s", code, body)
		}
		mockBefore := mockCalls
		if r, err := h.Handle(ctx, sandboxCharge(liveOnly, uuid.New(), "cardUser:cardToken")); err != nil || r.GetPaymentSucceeded().GetPspChargeId() != "mock_ch" || mockCalls != mockBefore+1 || fake.count("/payment/auth") != before {
			t.Errorf("sandbox charge of a tenant with a live account only: %v, %v", r, err)
		}
	})

	t.Run("a stored sandbox account that points at the provider's live mode is not used", func(t *testing.T) {
		stale := uuid.New()
		bad := `{"api_key":"a","secret_key":"s","base_url":"` + iyzico.LiveURL + `",` + buyer + `}`
		if err := acct.Put(ctx, stale, "iyzico", "sandbox", []byte(bad), true); err != nil {
			t.Fatal(err)
		}
		before := fake.count("/payment/auth")
		r, err := h.Handle(ctx, sandboxCharge(stale, uuid.New(), "cardUser:cardToken"))
		if err != nil || r.GetPaymentErrored().GetCode() != handler.CodeNotConfigured || fake.count("/payment/auth") != before {
			t.Errorf("reply = %v, %v; a sandbox command must never reach the live host", r, err)
		}
	})

	t.Run("removing the connection puts the tenant back on the mock", func(t *testing.T) {
		if code, _ := do("DELETE", base+"/provider-accounts/iyzico?environment=sandbox", ""); code != 204 {
			t.Fatalf("delete: %d", code)
		}
		mockBefore := mockCalls
		if r, err := h.Handle(ctx, sandboxCharge(tenant, uuid.New(), "cardUser:cardToken")); err != nil || r.GetPaymentSucceeded().GetPspChargeId() != "mock_ch" || mockCalls != mockBefore+1 {
			t.Errorf("after the delete: %v, %v", r, err)
		}
	})
}

func mux(h *accounts.Handler) http.Handler {
	m := http.NewServeMux()
	h.Register(m, func(f http.HandlerFunc) http.HandlerFunc { return f })
	return m
}
