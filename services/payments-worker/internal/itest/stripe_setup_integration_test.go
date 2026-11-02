//go:build integration

package itest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/stripe"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// A customer saves a card on Stripe's page and Payrudder learns it from the notification: the page is made on a Stripe Customer that is remembered (and not made twice),
// the completed setup session is believed only when signed, the method becomes "customer:method" on the customer, and a notification Stripe has to repeat (plans-svc was
// down) is handled the second time.
func TestStripeSavedCardBecomesTheCustomersPaymentMethod(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	pool := cdb.DB
	ctx := context.Background()

	var mu sync.Mutex
	var calls []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/customers":
			_, _ = io.WriteString(w, `{"id":"cus_77"}`)
		case "POST /v1/checkout/sessions":
			_, _ = io.WriteString(w, `{"id":"cs_1","url":"https://checkout.stripe.com/c/pay/cs_1"}`)
		case "GET /v1/setup_intents/seti_1":
			_, _ = io.WriteString(w, `{"status":"succeeded","payment_method":"pm_card","customer":"cus_77"}`)
		case "GET /v1/setup_intents/seti_2":
			_, _ = io.WriteString(w, `{"status":"succeeded","payment_method":"pm_new","customer":"cus_77"}`)
		case "GET /v1/setup_intents/seti_0":
			_, _ = io.WriteString(w, `{"status":"succeeded","payment_method":"pm_old","customer":"cus_77"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	count := func(call string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, c := range calls {
			if c == call {
				n++
			}
		}
		return n
	}

	box, _ := secretbox.New(bytes.Repeat([]byte{5}, 32))
	registry := payments.NewRegistry()
	stripe.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	plans := &recPlans{currency: "EUR", customers: map[string]*plansv1.Customer{"user-42": {Id: "cccccccc-0000-0000-0000-000000000042", ExternalId: "user-42", Name: "Ada", Email: "ada@example.com"}}}
	objects := store.Objects{DB: pool}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: store.Attempts{DB: pool},
		Ledger: store.Ledger{DB: pool}, Subscriptions: store.Subscriptions{DB: pool}, Objects: objects, Methods: store.PaymentMethods{DB: pool}, Log: log, Token: "internal", Plans: plans}).Handler())
	defer api.Close()

	tenant := uuid.New()
	const secret = "whsec_setup"
	if err := accounts.Put(ctx, tenant, "stripe", "live", []byte(`{"api_key":"sk_live_abc","webhook_secret":"`+secret+`","base_url":"`+fake.URL+`"}`), true); err != nil {
		t.Fatal(err)
	}
	setup := func(external, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("POST", api.URL+"/admin/tenants/"+tenant.String()+"/providers/stripe/customers/"+external+"/payment-method-setup?environment=live", strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	hookAt := func(id, session string, created int) int {
		t.Helper()
		body := `{"id":"` + id + `","type":"checkout.session.completed","created":` + strconv.Itoa(created) + `,"data":{"object":` + session + `}}`
		req, _ := http.NewRequest("POST", api.URL+"/webhooks/stripe/"+tenant.String()+"/live", strings.NewReader(body))
		req.Header.Set("Stripe-Signature", stripe.SignatureHeader(secret, time.Now(), []byte(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	hook := func(id, session string) int { return hookAt(id, session, 1790000000) }
	method := func() string {
		plans.mu.Lock()
		defer plans.mu.Unlock()
		return plans.methods["user-42"]
	}

	// The page: a Stripe Customer is made once and remembered, whatever the number of pages.
	for i := 0; i < 2; i++ {
		code, body := setup("user-42", `{"return_url":"https://pay.example.com/back"}`)
		if code != http.StatusCreated || !strings.Contains(body, "https://checkout.stripe.com/c/pay/cs_1") {
			t.Fatalf("setup %d: %d %s", i, code, body)
		}
	}
	if n := count("POST /v1/customers"); n != 1 || count("POST /v1/checkout/sessions") != 2 {
		t.Errorf("customers made %d, sessions %d: the customer is made once and remembered", n, count("POST /v1/checkout/sessions"))
	}
	if ref, ok, err := objects.Get(ctx, tenant, "stripe", "live", "customer", "cus:cccccccc-0000-0000-0000-000000000042"); err != nil || !ok || ref != "cus_77" {
		t.Errorf("remembered customer = %q %v %v", ref, ok, err)
	}
	if code, _ := setup("nobody", `{"return_url":"https://pay.example.com/back"}`); code != http.StatusNotFound {
		t.Errorf("an unknown customer: %d, want 404", code)
	}
	if code, _ := setup("user-42", `{"return_url":"http://pay.example.com/back"}`); code != http.StatusBadRequest {
		t.Errorf("an http return address: %d, want 400", code)
	}

	// Nothing is set by making the page, nor by a session of somebody else's.
	ours := `{"id":"cs_1","mode":"setup","customer":"cus_77","setup_intent":"seti_1","payment_status":"no_payment_required","metadata":{"payrudder_customer_id":"cccccccc-0000-0000-0000-000000000042","payrudder_external_id":"user-42"}}`
	if got := method(); got != "" {
		t.Fatalf("method before the card was saved: %q", got)
	}
	if code := hook("evt_foreign", strings.Replace(ours, `"metadata":{"payrudder_customer_id":"cccccccc-0000-0000-0000-000000000042","payrudder_external_id":"user-42"}`, `"metadata":{}`, 1)); code != http.StatusOK || method() != "" {
		t.Fatalf("a session Payrudder did not make: %d, method %q", code, method())
	}

	// plans-svc is down for the first delivery: Stripe is told to try again, and the repeat sets the method.
	plans.mu.Lock()
	plans.fail = true
	plans.mu.Unlock()
	if code := hook("evt_saved", ours); code != http.StatusServiceUnavailable {
		t.Fatalf("with plans-svc down: %d, want 503", code)
	}
	plans.mu.Lock()
	plans.fail = false
	plans.mu.Unlock()
	if code := hook("evt_saved", ours); code != http.StatusOK || method() != "cus_77:pm_card" {
		t.Fatalf("the repeat: %d, method %q, want cus_77:pm_card", code, method())
	}
	// A duplicate delivery is a duplicate: it sets nothing again.
	plans.mu.Lock()
	plans.methods["user-42"] = "changed-by-the-tenant"
	plans.mu.Unlock()
	if code := hook("evt_saved", ours); code != http.StatusOK || method() != "changed-by-the-tenant" {
		t.Errorf("a duplicate: %d, method %q", code, method())
	}
	// Newest wins: a later card is applied, and a notification Stripe sends late for an earlier card does not bring it back (its answer is still 200: nothing to retry).
	if code := hookAt("evt_newer", strings.Replace(ours, "seti_1", "seti_2", 1), 1790000100); code != http.StatusOK || method() != "cus_77:pm_new" {
		t.Fatalf("a later card: %d, method %q, want cus_77:pm_new", code, method())
	}
	if code := hookAt("evt_older", strings.Replace(ours, "seti_1", "seti_0", 1), 1789999000); code != http.StatusOK || method() != "cus_77:pm_new" {
		t.Errorf("an earlier card arriving late: %d, method %q, want cus_77:pm_new kept", code, method())
	}
	// Unsigned, it is not believed.
	req, _ := http.NewRequest("POST", api.URL+"/webhooks/stripe/"+tenant.String()+"/live", strings.NewReader(`{"id":"evt_forged","type":"checkout.session.completed","data":{"object":`+ours+`}}`))
	req.Header.Set("Stripe-Signature", stripe.SignatureHeader("whsec_other", time.Now(), []byte(`{}`)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a forged notification: %d, want 401", resp.StatusCode)
	}
}
