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
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/pkg/payments/iyzico"
	"github.com/0furkancolak/quotify/pkg/payments/paddle"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/secretbox"
	"github.com/0furkancolak/quotify/pkg/testkit"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/handler"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/webhook"
	"github.com/0furkancolak/quotify/services/payments-worker/migrations"
)

func (r *recPlans) GetPlanVersion(_ context.Context, _ uuid.UUID, req *plansv1.GetPlanVersionRequest) (*plansv1.PlanVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pv, ok := r.versions[req.GetPlanCode()+req.GetPlanVersionId()]; ok {
		return pv, nil
	}
	return nil, status.Error(codes.NotFound, "no such plan version")
}

func (r *recPlans) GetCustomer(_ context.Context, _ uuid.UUID, ext string) (*plansv1.Customer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.customers[ext]; ok {
		return c, nil
	}
	return nil, status.Error(codes.NotFound, "no such customer")
}

func (r *recPlans) TenantCurrency(context.Context, uuid.UUID) (string, error) {
	return r.currency, nil
}

// apiPaddle answers the API calls the worker makes for the provider API, by "METHOD path", and records them.
type apiPaddle struct {
	mu     sync.Mutex
	srv    *httptest.Server
	routes map[string]string
	calls  []string
	bodies map[string][]map[string]any
	nextPr int
}

func newAPIPaddle(t *testing.T) *apiPaddle {
	f := &apiPaddle{routes: map[string]string{}, bodies: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		key := r.Method + " " + r.URL.Path
		f.calls = append(f.calls, key)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.bodies[key] = append(f.bodies[key], body)
		if key == "POST /prices" {
			f.nextPr++
			_, _ = io.WriteString(w, `{"data":{"id":"pri_0`+string(rune('0'+f.nextPr))+`"}}`)
			return
		}
		if reply, ok := f.routes[key]; ok {
			_, _ = io.WriteString(w, reply)
			return
		}
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","detail":"no route"}}`)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *apiPaddle) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == key {
			n++
		}
	}
	return n
}

func (f *apiPaddle) body(key string, i int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key][i]
}

func TestProviderAPIThroughTheWorker(t *testing.T) {
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
	fake := newAPIPaddle(t)

	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	paddle.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pvPro := &plansv1.PlanVersion{Id: "11111111-aaaa-aaaa-aaaa-111111111111", PlanCode: "pro", Version: 1, BaseFeeMinor: 1900, Fees: []*plansv1.CurrencyFee{
		{Currency: "USD", BaseFeeMinor: 19000, Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR},
		{Currency: "TRY", BaseFeeMinor: 59900, Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH},
	}}
	pvBasic := &plansv1.PlanVersion{Id: "22222222-bbbb-bbbb-bbbb-222222222222", PlanCode: "basic", Version: 1, BaseFeeMinor: 900}
	plans := &recPlans{currency: "USD",
		versions:  map[string]*plansv1.PlanVersion{"pro": pvPro, "basic": pvBasic, pvPro.Id: pvPro, pvBasic.Id: pvBasic},
		customers: map[string]*plansv1.Customer{"cus_1": {Id: "cccccccc-0000-0000-0000-000000000001", ExternalId: "cus_1", Name: "Ada", Email: "ada@example.com", Country: "DE"}},
		sub: &plansv1.Subscription{Id: "33333333-0000-0000-0000-000000000003", Provider: "paddle", ProviderRef: "sub_77", Owner: "provider", Currency: "USD",
			PlanVersionId: pvBasic.Id, Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH}}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: store.Attempts{DB: pool},
		Ledger: store.Ledger{DB: pool}, Subscriptions: store.Subscriptions{DB: pool}, Objects: store.Objects{DB: pool}, Log: log, Token: "internal", Plans: plans}).Handler())
	defer api.Close()

	tenant := uuid.New()
	settings := `{"api_key":"pdl_secret","base_url":"` + fake.srv.URL + `","webhook_secret":"whsec","usage_product_id":"pro_usage"}`
	if err := accounts.Put(ctx, tenant, "paddle", "live", []byte(settings), true); err != nil {
		t.Fatal(err)
	}
	base := "/admin/tenants/" + tenant.String() + "/providers/paddle"
	do := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+base+path+"?environment=live", strings.NewReader(body))
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

	t.Run("syncing the catalogue creates the product and a price per interval and currency, once", func(t *testing.T) {
		fake.mu.Lock()
		fake.routes["POST /products"] = `{"data":{"id":"pro_plan"}}`
		fake.mu.Unlock()
		code, out := do("POST", "/catalogue/sync", `{"plan_code":"pro"}`)
		if code != 200 {
			t.Fatalf("sync = %d %v", code, out)
		}
		created := out["created"].(map[string]any)
		skipped := out["skipped"].(map[string]any)
		if len(created) != 2 || created[pvPro.Id+"|month|USD"] == nil || created[pvPro.Id+"|year|USD"] == nil || len(skipped) != 1 || skipped[pvPro.Id+"|month|TRY"] == nil {
			t.Fatalf("created %v, skipped %v; want monthly and yearly USD created and TRY skipped", created, skipped)
		}
		if fake.count("POST /products") != 1 || fake.count("POST /prices") != 2 {
			t.Fatalf("%d product(s) and %d price(s) were created", fake.count("POST /products"), fake.count("POST /prices"))
		}
		// A second sync finds everything in place and creates nothing.
		code, out = do("POST", "/catalogue/sync", `{"plan_code":"pro"}`)
		if code != 200 || len(out["created"].(map[string]any)) != 0 || len(out["existing"].(map[string]any)) != 2 {
			t.Fatalf("second sync = %d %v", code, out)
		}
		if fake.count("POST /products") != 1 || fake.count("POST /prices") != 2 {
			t.Errorf("a second sync created more: %d product(s), %d price(s)", fake.count("POST /products"), fake.count("POST /prices"))
		}
		code, out = do("GET", "/catalogue", "")
		if code != 200 || len(out["objects"].([]any)) != 3 {
			t.Errorf("catalogue = %d %v; want a product and two prices", code, out)
		}
	})

	t.Run("a webhook for a price Payrudder created names its plan version and interval", func(t *testing.T) {
		yearRef := ""
		_, out := do("GET", "/catalogue", "")
		for _, o := range out["objects"].([]any) {
			m := o.(map[string]any)
			if m["key"] == pvPro.Id+"|year|USD" {
				yearRef = m["ref"].(string)
			}
		}
		if yearRef == "" {
			t.Fatal("no yearly price in the catalogue")
		}
		key, ok, err := (store.Objects{DB: pool}).ByRef(ctx, tenant, "paddle", "live", "price", yearRef)
		if err != nil || !ok || key != pvPro.Id+"|year|USD" {
			t.Fatalf("ByRef = %q, %v, %v", key, ok, err)
		}
	})

	t.Run("a Paddle event for a price Payrudder created makes plans-svc follow the plan and interval", func(t *testing.T) {
		_, out := do("GET", "/catalogue", "")
		yearRef := ""
		for _, o := range out["objects"].([]any) {
			if m := o.(map[string]any); m["key"] == pvPro.Id+"|year|USD" {
				yearRef = m["ref"].(string)
			}
		}
		body := `{"event_id":"evt_follow","event_type":"subscription.updated","occurred_at":"` + time.Now().UTC().Format(time.RFC3339) + `","data":{"id":"sub_77","status":"active",` +
			`"custom_data":{"quotify_subscription_id":"` + plans.sub.Id + `"},"items":[{"price":{"id":"` + yearRef + `"}}]}}`
		req, _ := http.NewRequest("POST", api.URL+"/webhooks/paddle/"+tenant.String()+"/live", strings.NewReader(body))
		req.Header.Set("Paddle-Signature", paddle.SignatureHeader("whsec", time.Now(), []byte(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("webhook = %d", resp.StatusCode)
		}
		plans.mu.Lock()
		last := plans.reqs[len(plans.reqs)-1]
		plans.mu.Unlock()
		if last.GetPlanVersionId() != pvPro.Id || last.GetInterval() != plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR || last.GetProviderStatus() != "active" {
			t.Errorf("plans-svc was told %v", last)
		}
	})

	t.Run("a change is priced and made with the catalogue's price; a plan version without one is refused", func(t *testing.T) {
		fake.mu.Lock()
		fake.routes["PATCH /subscriptions/sub_77/preview"] = `{"data":{"currency_code":"USD","update_summary":{"credit":{"amount":"300","currency_code":"USD"},"charge":{"amount":"900","currency_code":"USD"},"result":{"action":"charge","amount":"600"}}}}`
		fake.routes["PATCH /subscriptions/sub_77"] = `{"data":{}}`
		fake.mu.Unlock()
		code, out := do("POST", "/subscriptions/"+plans.sub.Id+"/change-preview", `{"plan_code":"pro","interval":"SUBSCRIPTION_INTERVAL_YEAR"}`)
		if code != 200 || out["net_minor"] != "600" || out["credit_minor"] != "300" || out["currency"] != "USD" || out["by_provider"] != true {
			t.Fatalf("preview = %d %v", code, out)
		}
		item := fake.body("PATCH /subscriptions/sub_77/preview", 0)["items"].([]any)[0].(map[string]any)
		if item["price_id"] == "" || item["price_id"] == nil {
			t.Errorf("the preview used no price: %v", item)
		}
		if fake.count("PATCH /subscriptions/sub_77") != 0 {
			t.Error("a preview changed the subscription")
		}
		code, out = do("POST", "/subscriptions/"+plans.sub.Id+"/change", `{"plan_code":"pro","interval":"SUBSCRIPTION_INTERVAL_YEAR","proration":"CHANGE_PRORATION_FULL"}`)
		if code != 200 || out["changed_at_provider"] != true || out["followed"] != true {
			t.Fatalf("change = %d %v", code, out)
		}
		if b := fake.body("PATCH /subscriptions/sub_77", 0); b["proration_billing_mode"] != "full_immediately" {
			t.Errorf("proration mode = %v", b["proration_billing_mode"])
		}
		plans.mu.Lock()
		last := plans.reqs[len(plans.reqs)-1]
		plans.mu.Unlock()
		if last.GetPlanVersionId() != pvPro.Id || last.GetInterval() != plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR || last.GetOwner() != "provider" {
			t.Errorf("plans-svc was told %v", last)
		}
		// Basic was never synced: no price, no change, nothing sent.
		before := fake.count("PATCH /subscriptions/sub_77")
		plans.sub.PlanVersionId = pvPro.Id
		code, out = do("POST", "/subscriptions/"+plans.sub.Id+"/change", `{"plan_code":"basic"}`)
		if code != 409 || !strings.Contains(out["error"].(map[string]any)["message"].(string), "sync the catalogue") || fake.count("PATCH /subscriptions/sub_77") != before {
			t.Fatalf("change to an unsynced plan = %d %v", code, out)
		}
		// A change for the end of the period is something Paddle cannot do.
		code, _ = do("POST", "/subscriptions/"+plans.sub.Id+"/change", `{"plan_code":"pro","interval":"SUBSCRIPTION_INTERVAL_YEAR","timing":"CHANGE_TIMING_PERIOD_END"}`)
		if code != 409 {
			t.Errorf("a change at the period end = %d; want 409", code)
		}
		plans.sub.PlanVersionId = pvBasic.Id
	})

	t.Run("a provider change that plans-svc cannot follow is accepted and says so", func(t *testing.T) {
		plans.mu.Lock()
		plans.fail = true
		plans.mu.Unlock()
		defer func() { plans.mu.Lock(); plans.fail = false; plans.mu.Unlock() }()
		code, out := do("POST", "/subscriptions/"+plans.sub.Id+"/change", `{"plan_code":"pro","interval":"SUBSCRIPTION_INTERVAL_YEAR"}`)
		if code != 202 || out["changed_at_provider"] != true {
			t.Fatalf("change = %d %v", code, out)
		}
	})

	t.Run("pause, resume, links, customers, discounts and invoices", func(t *testing.T) {
		fake.mu.Lock()
		for k, v := range map[string]string{
			"POST /subscriptions/sub_77/pause":                            `{"data":{}}`,
			"POST /subscriptions/sub_77/resume":                           `{"data":{}}`,
			"GET /subscriptions/sub_77/update-payment-method-transaction": `{"data":{"checkout":{"url":"https://pay.example/upd"}}}`,
			"GET /customers":                        `{"data":[]}`,
			"POST /customers":                       `{"data":{"id":"ctm_1"}}`,
			"GET /customers/ctm_1/addresses":        `{"data":[]}`,
			"POST /customers/ctm_1/addresses":       `{"data":{"id":"add_1"}}`,
			"POST /customers/ctm_1/portal-sessions": `{"data":{"urls":{"general":{"overview":"https://portal.example/p"}}}}`,
			"POST /discounts":                       `{"data":{"id":"dsc_1","code":"HELLO"}}`,
			"GET /transactions/txn_5/invoice":       `{"data":{"url":"https://files.example/i.pdf"}}`,
		} {
			fake.routes[k] = v
		}
		fake.mu.Unlock()
		if code, out := do("POST", "/subscriptions/"+plans.sub.Id+"/pause", `{"at_period_end":true}`); code != 202 || out["requested"] != true {
			t.Errorf("pause = %d %v", code, out)
		}
		if b := fake.body("POST /subscriptions/sub_77/pause", 0); b["effective_from"] != "next_billing_period" {
			t.Errorf("pause body = %v", b)
		}
		if code, _ := do("POST", "/subscriptions/"+plans.sub.Id+"/resume", ``); code != 202 {
			t.Errorf("resume = %d", code)
		}
		if code, out := do("POST", "/subscriptions/"+plans.sub.Id+"/payment-method-link", ``); code != 200 || out["url"] != "https://pay.example/upd" {
			t.Errorf("payment method link = %d %v", code, out)
		}
		if code, out := do("POST", "/customers/cus_1/portal", ``); code != 200 || out["url"] != "https://portal.example/p" {
			t.Errorf("portal = %d %v", code, out)
		}
		if b := fake.body("POST /customers", 0); b["email"] != "ada@example.com" || b["custom_data"].(map[string]any)["quotify_customer_id"] != "cccccccc-0000-0000-0000-000000000001" {
			t.Errorf("customer body = %v", b)
		}
		if code, _ := do("POST", "/customers/nobody/portal", ``); code != 404 {
			t.Errorf("portal for an unknown customer = %d", code)
		}
		if code, out := do("POST", "/discounts", `{"Description":"Hello","Type":"percentage","Amount":"10","Code":"HELLO"}`); code != 201 || out["discount_ref"] != "dsc_1" {
			t.Errorf("discount = %d %v", code, out)
		}
		if code, out := do("GET", "/transactions/txn_5/invoice", ``); code != 200 || out["url"] != "https://files.example/i.pdf" {
			t.Errorf("invoice = %d %v", code, out)
		}
		// A subscription another provider runs is not driven through this one.
		plans.sub.Provider = "iyzico"
		if code, _ := do("POST", "/subscriptions/"+plans.sub.Id+"/pause", ``); code != 409 {
			t.Errorf("pause of a subscription Paddle does not run = %d", code)
		}
		plans.sub.Provider = "paddle"
	})

	t.Run("a provider without the interface answers 501, and the token is needed", func(t *testing.T) {
		if err := accounts.Put(ctx, tenant, "iyzico", "live", []byte(`{"api_key":"a","secret_key":"s","base_url":"http://127.0.0.1:1"}`), false); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("POST", api.URL+"/admin/tenants/"+tenant.String()+"/providers/iyzico/catalogue/sync?environment=live", strings.NewReader(`{"plan_code":"pro"}`))
		req.Header.Set("X-Internal-Token", "internal")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("iyzico catalogue = %d; want 501", resp.StatusCode)
		}
		req, _ = http.NewRequest("GET", api.URL+base+"/catalogue?environment=live", nil)
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
			t.Errorf("without the token = %d", resp.StatusCode)
		}
	})
	_ = time.Second
}
