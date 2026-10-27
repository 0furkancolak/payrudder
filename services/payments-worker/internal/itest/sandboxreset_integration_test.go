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

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	paymentsreset "github.com/0furkancolak/payrudder/services/payments-worker/internal/sandboxreset"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// A sandbox reset takes a tenant's attempts and ledger (append-only for the product) and keeps its provider accounts; it is refused for anything but a sandbox
// tenant, needs the internal token, leaves another tenant alone and finds nothing the second time.
func TestSandboxResetEndpoint(t *testing.T) {
	pg := testkit.StartPostgres(t)
	ctx := context.Background()
	cdb := openDB(t, pg)
	pool := cdb.DB
	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	attempts, ledger := store.Attempts{DB: pool}, store.Ledger{DB: pool}
	accounts := store.Accounts{DB: pool, Box: box}
	api := httptest.NewServer((&webhook.Server{Registry: payments.NewRegistry(), Accounts: accounts, Attempts: attempts, Ledger: ledger,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Token: "internal", Reset: paymentsreset.New(pool).Reset}).Handler())
	defer api.Close()

	sbx, other := uuid.New(), uuid.New()
	for _, tid := range []uuid.UUID{sbx, other} {
		invoice := uuid.New()
		if _, err := attempts.Start(ctx, uuid.New(), tid, invoice, "charge", "sandbox", "mockpsp", invoice.String()+":charge:0", map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(ctx, store.LedgerEntry{TenantID: tid, InvoiceID: invoice, Provider: "mockpsp", Environment: "sandbox", Kind: "charge", Status: "succeeded",
			AmountMinor: 100, Currency: "USD", ProviderRef: "ch_" + invoice.String(), Source: "command"}); err != nil {
			t.Fatal(err)
		}
		if err := accounts.Put(ctx, tid, "banktransfer", "sandbox", []byte(`{"bank_name":"X","account_holder":"Y","iban":"DE89370400440532013000"}`), false); err != nil {
			t.Fatal(err)
		}
	}
	count := func(table string, tid uuid.UUID) int {
		t.Helper()
		q := map[string]string{"payment_attempts": `SELECT count(*) FROM payment_attempts WHERE tenant_id = $1`, "payment_ledger": `SELECT count(*) FROM payment_ledger WHERE tenant_id = $1`,
			"provider_accounts": `SELECT count(*) FROM provider_accounts WHERE tenant_id = $1`}[table]
		var n int
		if err := cdb.At(tid).QueryRow(ctx, q, tid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	call := func(tenantID uuid.UUID, token, body string) (int, string) {
		req, _ := http.NewRequest("POST", api.URL+"/admin/tenants/"+tenantID.String()+"/sandbox-reset", strings.NewReader(body))
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

	if code, _ := call(sbx, "", `{"environment":"sandbox"}`); code != 401 {
		t.Errorf("without the internal token: %d", code)
	}
	for _, body := range []string{`{"environment":"live"}`, `{}`, `{"environment":"staging"}`} {
		if code, out := call(sbx, "internal", body); code != 409 || !strings.Contains(out, "admin.sandbox.live_refused") {
			t.Errorf("%s: %d %s, want a refusal with its key", body, code, out)
		}
	}
	if count("payment_ledger", sbx) != 1 {
		t.Fatal("a refused reset deleted rows")
	}

	code, out := call(sbx, "internal", `{"environment":"sandbox"}`)
	if code != 200 {
		t.Fatalf("reset: %d %s", code, out)
	}
	var res struct{ Deleted map[string]int64 }
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Deleted["payment_ledger"] != 1 || res.Deleted["payment_attempts"] != 1 {
		t.Errorf("answer = %s (%v)", out, err)
	}
	if count("payment_ledger", sbx) != 0 || count("payment_attempts", sbx) != 0 {
		t.Error("the tenant still has attempts or ledger rows")
	}
	if count("provider_accounts", sbx) != 1 {
		t.Error("the provider account of the tenant was deleted")
	}
	if count("payment_ledger", other) != 1 || count("payment_attempts", other) != 1 {
		t.Error("another tenant's rows were deleted")
	}
	if code, out := call(sbx, "internal", `{"environment":"sandbox"}`); code != 200 || strings.Contains(out, `"payment_ledger"`) {
		t.Errorf("a second reset: %d %s", code, out)
	}
	missing, err := sandboxreset.MissingTables(ctx, cdb.Pooled, paymentsreset.Tables, paymentsreset.Kept)
	if err != nil || len(missing) != 0 {
		t.Errorf("tables a reset does not cover: %v, %v", missing, err)
	}
}
