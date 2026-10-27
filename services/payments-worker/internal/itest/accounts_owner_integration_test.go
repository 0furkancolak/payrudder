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
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// A person connects their own live provider account (the gateway sets ?verify=true and the actor): only the provider's own hosts are allowed, the provider has
// to accept the credentials, every change is in the audit trail without a secret, and the go-live answer says which account works and why one does not.
func TestOwnerProviderConnection(t *testing.T) {
	pg := testkit.StartPostgres(t)
	ctx := context.Background()
	cdb := openDB(t, pg)
	pool := cdb.DB
	fake := newFakeIyzico(t)
	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	banktransfer.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: store.Attempts{DB: pool},
		Ledger: store.Ledger{DB: pool}, Log: log, Token: "internal"}).Handler())
	defer api.Close()

	do := func(method, path, body, actor string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		if actor != "" {
			req.Header.Set("X-Payrudder-Actor", actor)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	events := func(tenant uuid.UUID) []string {
		t.Helper()
		rows, err := cdb.At(tenant).Query(ctx, `SELECT provider || '/' || environment || '/' || action || '/' || actor || '/' || verified_remote::text FROM provider_account_events WHERE tenant_id = $1 ORDER BY id`, tenant)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	iyzicoSettings := `{"api_key":"api","secret_key":"OWNER-SECRET-KEY","base_url":"` + fake.srv.URL + `","buyer":{"name":"Ada","surname":"L","identity_number":"11111111111","address":"x","city":"Istanbul","country":"Turkey","zip_code":"34000","ip":"1.2.3.4","email":"b@example.com"}}`
	bankSettings := `{"bank_name":"Bank","account_holder":"Ada Labs","iban":"DE89370400440532013000","currencies":["EUR"]}`
	actor := "user:" + uuid.NewString()

	t.Run("a person may not point an account at a host of their own", func(t *testing.T) {
		tenant := uuid.New()
		code, body := do("PUT", "/admin/tenants/"+tenant.String()+"/provider-accounts/iyzico?environment=live&verify=true", iyzicoSettings, actor)
		if code != 400 || !strings.Contains(body, "payments.account.endpoint_not_allowed") || strings.Contains(body, "OWNER-SECRET-KEY") {
			t.Fatalf("a custom host: %d %s", code, body)
		}
		if fake.count("/payment/bin/check") != 0 {
			t.Error("the secret keys were sent to a host the person named")
		}
		if n := len(events(tenant)); n != 0 {
			t.Errorf("a refused account left %d audit rows", n)
		}
		// Support may (a proxy, a test double): the same body without verify is accepted, as before.
		if code, body := do("PUT", "/admin/tenants/"+tenant.String()+"/provider-accounts/iyzico?environment=live", iyzicoSettings, ""); code != 200 {
			t.Fatalf("operator put: %d %s", code, body)
		}
		if got := events(tenant); len(got) != 1 || got[0] != "iyzico/live/connected/operator/false" {
			t.Errorf("operator audit = %v", got)
		}
	})

	t.Run("bank details are connected, audited with the person, and removed", func(t *testing.T) {
		tenant := uuid.New()
		code, body := do("PUT", "/admin/tenants/"+tenant.String()+"/provider-accounts/banktransfer?environment=live&verify=true&default=true", bankSettings, actor)
		if code != 200 || strings.Contains(body, "Ada Labs") {
			t.Fatalf("put: %d %s", code, body)
		}
		if code, body := do("PUT", "/admin/tenants/"+tenant.String()+"/provider-accounts/banktransfer?environment=live&verify=true&default=true", bankSettings, actor); code != 200 {
			t.Fatalf("replace: %d %s", code, body)
		}
		code, body = do("GET", "/admin/tenants/"+tenant.String()+"/go-live", "", "")
		var gl struct {
			Ready    bool `json:"ready"`
			Accounts []struct {
				Provider string `json:"provider"`
				Default  bool   `json:"default"`
				Working  bool   `json:"working"`
				Checked  bool   `json:"checked"`
				Problem  string `json:"problem"`
			} `json:"accounts"`
		}
		if err := json.Unmarshal([]byte(body), &gl); err != nil || code != 200 {
			t.Fatalf("go-live: %d %s %v", code, body, err)
		}
		if !gl.Ready || len(gl.Accounts) != 1 || !gl.Accounts[0].Working || !gl.Accounts[0].Default || gl.Accounts[0].Checked || strings.Contains(body, "DE89") {
			t.Errorf("go-live = %s", body)
		}
		if code, _ := do("DELETE", "/admin/tenants/"+tenant.String()+"/provider-accounts/banktransfer?environment=live", "", actor); code != 204 {
			t.Fatalf("delete: %d", code)
		}
		if code, _ := do("DELETE", "/admin/tenants/"+tenant.String()+"/provider-accounts/banktransfer?environment=live", "", actor); code != 404 {
			t.Fatalf("deleting twice: %d", code)
		}
		want := []string{"banktransfer/live/connected/" + actor + "/false", "banktransfer/live/replaced/" + actor + "/false", "banktransfer/live/removed/" + actor + "/false"}
		if got := events(tenant); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("audit = %v, want %v", got, want)
		}
		secretsInAudit, err := cdb.CountAll(ctx, `SELECT count(*) FROM provider_account_events WHERE actor LIKE '%Ada%' OR provider LIKE '%DE89%'`)
		if err != nil || secretsInAudit != 0 {
			t.Errorf("settings leaked into the audit trail: %d %v", secretsInAudit, err)
		}
	})

	t.Run("an account the provider no longer accepts does not count, and says why", func(t *testing.T) {
		tenant := uuid.New()
		if code, body := do("PUT", "/admin/tenants/"+tenant.String()+"/provider-accounts/iyzico?environment=live&default=true", iyzicoSettings, ""); code != 200 {
			t.Fatalf("put: %d %s", code, body)
		}
		get := func() (ready bool, problem string, checked bool) {
			t.Helper()
			_, body := do("GET", "/admin/tenants/"+tenant.String()+"/go-live", "", "")
			var gl struct {
				Ready    bool `json:"ready"`
				Accounts []struct {
					Checked bool   `json:"checked"`
					Problem string `json:"problem"`
				} `json:"accounts"`
			}
			if err := json.Unmarshal([]byte(body), &gl); err != nil || len(gl.Accounts) != 1 {
				t.Fatalf("go-live: %s %v", body, err)
			}
			return gl.Ready, gl.Accounts[0].Problem, gl.Accounts[0].Checked
		}
		if ready, problem, checked := get(); !ready || problem != "" || !checked {
			t.Fatalf("a working account: ready=%v problem=%q checked=%v", ready, problem, checked)
		}
		// The provider revokes the key. The answer is remembered for a short while per version of the settings, so a new version asks again.
		fake.mu.Lock()
		fake.bin = `{"status":"failure","errorCode":"1000","errorMessage":"invalid signature"}`
		fake.mu.Unlock()
		if code, body := do("PUT", "/admin/tenants/"+tenant.String()+"/provider-accounts/iyzico?environment=live&default=true", iyzicoSettings, ""); code != 200 {
			t.Fatalf("replace: %d %s", code, body)
		}
		if ready, problem, _ := get(); ready || problem != "refused" {
			t.Fatalf("a refused account: ready=%v problem=%q", ready, problem)
		}
	})
}
