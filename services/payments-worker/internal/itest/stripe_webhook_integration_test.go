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
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/stripe"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// An account connected with a key only (a test key, in the sandbox) gets its webhook endpoint made at Stripe and the signing secret Stripe shows stored, sealed, in the
// account: after that, and not before, a notification signed with it is believed.
func TestStripeWebhookEndpointIsRegisteredAndItsSecretStored(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	pool := cdb.DB
	ctx := context.Background()

	var mu sync.Mutex
	var created []url.Values
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/v1/webhook_endpoints" && r.Method == http.MethodPost && r.Header.Get("Authorization") == "Bearer sk_test_abc" {
			form, _ := url.ParseQuery(string(raw))
			created = append(created, form)
			_, _ = io.WriteString(w, `{"id":"we_123","object":"webhook_endpoint","secret":"whsec_fromstripe","enabled_events":["payment_intent.succeeded"]}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer fake.Close()

	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	registry := payments.NewRegistry()
	stripe.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: store.Attempts{DB: pool},
		Ledger: store.Ledger{DB: pool}, Subscriptions: store.Subscriptions{DB: pool}, Log: log, Token: "internal"}).Handler())
	defer api.Close()

	tenant := uuid.New()
	if err := accounts.Put(ctx, tenant, "stripe", "sandbox", []byte(`{"api_key":"sk_test_abc","base_url":"`+fake.URL+`"}`), true); err != nil {
		t.Fatal(err)
	}
	hook := "https://pay.example.com/webhooks/stripe/" + tenant.String() + "/sandbox"
	call := func(path, query, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("POST", api.URL+path+query, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	event := `{"id":"evt_1","type":"payment_intent.succeeded","created":1790000000,"data":{"object":{"id":"pi_1","amount":500,"amount_received":500,"currency":"eur","metadata":{"payrudder_key":"k1"}}}}`
	postEvent := func(secret string) int {
		req, _ := http.NewRequest("POST", api.URL+"/webhooks/stripe/"+tenant.String()+"/sandbox", strings.NewReader(event))
		req.Header.Set("Stripe-Signature", stripe.SignatureHeader(secret, time.Now(), []byte(event)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Before the endpoint exists no signature is good, not even one made with an empty key.
	if code := postEvent(""); code != http.StatusUnauthorized {
		t.Fatalf("a notification signed with an empty key: %d, want 401", code)
	}

	base := "/admin/tenants/" + tenant.String() + "/provider-accounts/stripe/webhook-endpoint"
	if code, body := call(base, "?environment=sandbox", `{"url":"http://insecure.example.com/hook"}`); code != http.StatusBadRequest {
		t.Fatalf("an http address: %d %s, want 400", code, body)
	}
	if code, body := call(base, "?environment=live", `{"url":"`+hook+`"}`); code != http.StatusNotFound && code != http.StatusBadRequest {
		t.Fatalf("the live environment has no account: %d %s", code, body)
	}
	code, body := call(base, "?environment=sandbox", `{"url":"`+hook+`"}`)
	if code != http.StatusCreated || !strings.Contains(body, "we_123") || strings.Contains(body, "whsec_") {
		t.Fatalf("register: %d %s; the endpoint id is answered and the secret never", code, body)
	}
	mu.Lock()
	if len(created) != 1 || created[0].Get("url") != hook || created[0].Get("api_version") != stripe.APIVersion {
		t.Fatalf("Stripe was asked for %v", created)
	}
	mu.Unlock()

	// The account was replaced, not added to: the secret is in it, the key is still there and it is still the default.
	settings, _, err := accounts.Settings(ctx, tenant, "stripe", "sandbox")
	var stored map[string]any
	if err != nil || json.Unmarshal(settings, &stored) != nil || stored["webhook_secret"] != "whsec_fromstripe" || stored["api_key"] != "sk_test_abc" {
		t.Fatalf("stored settings %s %v", settings, err)
	}
	if name, ok, _ := accounts.Default(ctx, tenant, "sandbox"); !ok || name != "stripe" {
		t.Errorf("default = %q %v, want stripe to stay the default", name, ok)
	}
	var sealed int
	if err := cdb.At(tenant).QueryRow(ctx, `SELECT count(*) FROM provider_accounts WHERE tenant_id = $1 AND position('whsec_fromstripe'::bytea in settings_enc) > 0`, tenant).Scan(&sealed); err != nil || sealed != 0 {
		t.Errorf("the secret is in the database in the clear (%d rows, %v)", sealed, err)
	}

	// Now the notification is believed when signed with the new secret, and not with another.
	if code := postEvent("whsec_other"); code != http.StatusUnauthorized {
		t.Errorf("a notification signed with another secret: %d, want 401", code)
	}
	if code := postEvent("whsec_fromstripe"); code != http.StatusOK {
		t.Errorf("a notification signed with the registered secret: %d, want 200", code)
	}
}
