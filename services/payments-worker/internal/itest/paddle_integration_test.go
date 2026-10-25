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
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
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

type fakePaddle struct {
	mu    sync.Mutex
	srv   *httptest.Server
	calls map[string]int
	last  map[string]any
	// failCancel makes cancelling a subscription fail.
	failCancel bool
	cancelBody map[string]any
}

func newFakePaddle(t *testing.T) *fakePaddle {
	f := &fakePaddle{calls: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls[r.Method+" "+r.URL.Path]++
		f.last = nil
		_ = json.Unmarshal(raw, &f.last)
		if r.Header.Get("Authorization") != "Bearer pdl_secret" {
			w.WriteHeader(403)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			if f.failCancel {
				w.WriteHeader(500)
				return
			}
			f.cancelBody = f.last
			_, _ = io.WriteString(w, `{"data":{}}`)
		case strings.HasSuffix(r.URL.Path, "/charge"):
			_, _ = io.WriteString(w, `{"data":{"id":"sub_01"}}`)
		case r.URL.Path == "/adjustments":
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"data":{"id":"adj_01"}}`)
		case r.URL.Path == "/transactions":
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"data":{"id":"txn_co","checkout":{"url":"https://pay.example/co"}}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePaddle) count(k string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[k]
}

// recPlans stands in for plans-svc.
type recPlans struct {
	mu   sync.Mutex
	reqs []*plansv1.SyncProviderSubscriptionRequest
	fail bool

	sub        *plansv1.Subscription                       // what GetSubscription answers
	started    []*plansv1.StartProviderMigrationRequest    // what StartProviderMigration saw
	completed  []*plansv1.CompleteProviderMigrationRequest // what CompleteProviderMigration saw
	aborted    []string                                    // migrations aborted
	completion *plansv1.CompleteProviderMigrationResponse  // what CompleteProviderMigration answers
	startErr   error
	bank       []*plansv1.BankTransferDetails // what SetBankTransfer saw, in order

	versions  map[string]*plansv1.PlanVersion // by plan code (GetPlanVersion by code) or id
	customers map[string]*plansv1.Customer    // by external id
	currency  string                          // the tenant's currency
}

func (r *recPlans) SyncProviderSubscription(_ context.Context, _ uuid.UUID, req *plansv1.SyncProviderSubscriptionRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("plans-svc is down")
	}
	r.reqs = append(r.reqs, req)
	return nil
}

func (r *recPlans) GetSubscription(context.Context, uuid.UUID, string) (*plansv1.Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sub, nil
}

func (r *recPlans) StartProviderMigration(_ context.Context, _ uuid.UUID, req *plansv1.StartProviderMigrationRequest) (*plansv1.ProviderMigration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return nil, r.startErr
	}
	r.started = append(r.started, req)
	m := &plansv1.ProviderMigration{Id: "11111111-1111-1111-1111-111111111111", SubscriptionId: req.GetSubscriptionId(), ToProvider: req.GetToProvider(),
		State: plansv1.ProviderMigration_STATE_STARTED}
	if cut := req.GetCutoverTime(); cut != nil {
		m.State, m.CutoverTime, m.NewSubscriptionId = plansv1.ProviderMigration_STATE_COMPLETED, cut, "22222222-2222-2222-2222-222222222222"
	}
	return m, nil
}

func (r *recPlans) CompleteProviderMigration(_ context.Context, _ uuid.UUID, req *plansv1.CompleteProviderMigrationRequest) (*plansv1.CompleteProviderMigrationResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return nil, errors.New("plans-svc is down")
	}
	r.completed = append(r.completed, req)
	if r.completion != nil {
		return r.completion, nil
	}
	return &plansv1.CompleteProviderMigrationResponse{Subscription: &plansv1.Subscription{Id: "22222222-2222-2222-2222-222222222222"}}, nil
}

// SetBankTransfer records the bank details handed to plans-svc; nil is a clear.
func (r *recPlans) SetBankTransfer(_ context.Context, _ uuid.UUID, d *plansv1.BankTransferDetails) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("plans-svc is down")
	}
	r.bank = append(r.bank, d)
	return nil
}

func (r *recPlans) AbortProviderMigration(_ context.Context, _ uuid.UUID, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aborted = append(r.aborted, id)
	return nil
}

func TestPaddleThroughTheWorker(t *testing.T) {
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
	fake := newFakePaddle(t)

	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	paddle.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	attempts, ledger := store.Attempts{DB: pool}, store.Ledger{DB: pool}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Attempts: attempts, Ledger: ledger, Resolver: resolver, Log: log}
	plans := &recPlans{}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: attempts,
		Ledger: ledger, Subscriptions: store.Subscriptions{DB: pool}, Log: log, Token: "internal", Plans: plans}).Handler())
	defer api.Close()

	tenant := uuid.New()
	const secret = "pdl_whsec"
	settings := `{"api_key":"pdl_secret","base_url":"` + fake.srv.URL + `","webhook_secret":"` + secret + `","usage_product_id":"pro_usage"}`
	if err := accounts.Put(ctx, tenant, "paddle", "live", []byte(settings), true); err != nil {
		t.Fatal(err)
	}
	hook := "/webhooks/paddle/" + tenant.String() + "/live"

	post := func(path, body string, header http.Header) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("POST", api.URL+path, strings.NewReader(body))
		for k, v := range header {
			req.Header[k] = v
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	signed := func(body string, at time.Time) http.Header {
		h := http.Header{}
		h.Set("Paddle-Signature", paddle.SignatureHeader(secret, at, []byte(body)))
		return h
	}
	event := func(id, typ, occurred, data string) string {
		return `{"event_id":"` + id + `","event_type":"` + typ + `","occurred_at":"` + occurred + `","data":` + data + `}`
	}
	admin := func(method, path string, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	invoice, command := uuid.New(), uuid.New()
	key := invoice.String() + ":charge:0"
	charge := &commandsv1.CommandEnvelope{CommandId: command.String(), SagaId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
		Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: invoice.String(), AmountMinor: 1116,
			Currency: "USD", PaymentMethod: "sub_01", IdempotencyKey: key}}}
	statusCmd := func(k string, inv uuid.UUID) *commandsv1.CommandEnvelope {
		return &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_QueryPaymentStatus{QueryPaymentStatus: &commandsv1.QueryPaymentStatus{InvoiceId: inv.String(), IdempotencyKey: k}}}
	}

	t.Run("a charge is accepted, not settled: it replies PENDING and writes nothing", func(t *testing.T) {
		r, err := h.Handle(ctx, charge)
		if err != nil || r.GetPaymentErrored().GetCode() != handler.CodePending || r.GetPaymentErrored().GetRetryable() {
			t.Fatalf("reply = %v, %v", r, err)
		}
		if fake.count("POST /subscriptions/sub_01/charge") != 1 {
			t.Fatalf("charge calls = %d", fake.count("POST /subscriptions/sub_01/charge"))
		}
		if rows, _ := ledger.List(ctx, tenant, 10); len(rows) != 0 {
			t.Fatalf("ledger = %+v; nothing is settled yet", rows)
		}
	})

	t.Run("the status is retried until the notification arrives", func(t *testing.T) {
		if r, err := h.Handle(ctx, statusCmd(key, invoice)); err == nil {
			t.Fatalf("got a reply %v; the query must be retried while the outcome is unknown", r)
		}
	})

	completed := func(id, k, txn string) string {
		return event(id, "transaction.completed", "2027-01-05T10:00:00Z", `{"id":"`+txn+`","status":"completed","currency_code":"USD","subscription_id":"sub_01",
			"items":[{"price":{"custom_data":{"quotify_key":"`+k+`"}},"quantity":1}],"details":{"totals":{"grand_total":"1116"}}}`)
	}

	t.Run("a bad signature is refused and does not use up the event id", func(t *testing.T) {
		body := completed("evt_1", key, "txn_01")
		if code, _ := post(hook, body, signed(body, time.Now().Add(-time.Hour))); code != 401 {
			t.Fatalf("stale signature: %d", code)
		}
		wrong := http.Header{}
		wrong.Set("Paddle-Signature", paddle.SignatureHeader("not-the-secret", time.Now(), []byte(body)))
		if code, _ := post(hook, body, wrong); code != 401 {
			t.Fatalf("wrong secret: %d", code)
		}
		if code, _ := post(hook, body, nil); code != 401 {
			t.Fatalf("no signature: %d", code)
		}
		if rows, _ := ledger.List(ctx, tenant, 10); len(rows) != 0 {
			t.Fatalf("a forged event reached the ledger: %+v", rows)
		}
	})

	t.Run("the signed notification settles the charge", func(t *testing.T) {
		body := completed("evt_1", key, "txn_01")
		code, resp := post(hook, body, signed(body, time.Now()))
		if code != 200 || !strings.Contains(resp, "processed") {
			t.Fatalf("webhook: %d %s", code, resp)
		}
		rows, _ := ledger.List(ctx, tenant, 10)
		if len(rows) != 1 || rows[0].Kind != "charge" || rows[0].Status != "succeeded" || *rows[0].ProviderRef != "txn_01" || rows[0].AmountMinor != 1116 ||
			rows[0].Currency != "USD" || rows[0].Source != "webhook" || rows[0].InvoiceID == nil || *rows[0].InvoiceID != invoice.String() {
			t.Fatalf("ledger = %+v", rows)
		}
		if code, resp = post(hook, body, signed(body, time.Now())); code != 200 || !strings.Contains(resp, "duplicate") {
			t.Fatalf("the same event again: %d %s", code, resp)
		}
		r, err := h.Handle(ctx, statusCmd(key, invoice))
		if err != nil || r.GetPaymentStatus().GetState() != commandsv1.PaymentStatus_STATE_SUCCEEDED || r.GetPaymentStatus().GetPspChargeId() != "txn_01" {
			t.Fatalf("status = %v, %v; the ledger has the outcome now", r, err)
		}
	})

	t.Run("a failed payment is a decline the status reports", func(t *testing.T) {
		inv2 := uuid.New()
		k2 := inv2.String() + ":charge:0"
		body := event("evt_2", "transaction.payment_failed", "2027-01-05T11:00:00Z", `{"id":"txn_02","currency_code":"USD","subscription_id":"sub_01",
			"items":[{"price":{"custom_data":{"quotify_key":"`+k2+`"}}}],"details":{"totals":{"grand_total":"500"}},"payments":[{"status":"error","error_code":"insufficient_funds"}]}`)
		if code, resp := post(hook, body, signed(body, time.Now())); code != 200 {
			t.Fatalf("webhook: %d %s", code, resp)
		}
		r, err := h.Handle(ctx, statusCmd(k2, inv2))
		if err != nil || r.GetPaymentStatus().GetState() != commandsv1.PaymentStatus_STATE_DECLINED || r.GetPaymentStatus().GetDeclineCode() != "insufficient_funds" {
			t.Fatalf("status = %v, %v", r, err)
		}
	})

	t.Run("Paddle's own renewals are recorded without an invoice", func(t *testing.T) {
		body := event("evt_3", "transaction.completed", "2027-02-01T00:00:00Z", `{"id":"txn_renew","currency_code":"USD","subscription_id":"sub_01",
			"items":[{"price":{"custom_data":null}}],"details":{"totals":{"grand_total":"2900"}}}`)
		if code, resp := post(hook, body, signed(body, time.Now())); code != 200 {
			t.Fatalf("webhook: %d %s", code, resp)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_ledger WHERE tenant_id = $1 AND provider_ref = 'txn_renew' AND invoice_id IS NULL AND amount_minor = 2900`, tenant).Scan(&n); err != nil || n != 1 {
			t.Fatalf("renewal rows = %d, %v", n, err)
		}
	})

	t.Run("subscription events are kept, newest event wins", func(t *testing.T) {
		qsub := uuid.New()
		created := event("evt_4", "subscription.created", "2027-01-05T12:00:00Z", `{"id":"sub_01","status":"active","custom_data":{"quotify_subscription_id":"`+qsub.String()+`"},"next_billed_at":"2027-02-01T00:00:00Z"}`)
		pastDue := event("evt_5", "subscription.past_due", "2027-02-02T12:00:00Z", `{"id":"sub_01","status":"past_due","custom_data":null}`)
		stale := event("evt_6", "subscription.updated", "2027-01-20T12:00:00Z", `{"id":"sub_01","status":"active","custom_data":null}`)
		// plans-svc is down for the first delivery: the provider is told to retry, and the retry goes through.
		plans.mu.Lock()
		plans.fail = true
		plans.mu.Unlock()
		if code, _ := post(hook, created, signed(created, time.Now())); code != 503 {
			t.Fatalf("with plans-svc down: %d, want 503", code)
		}
		plans.mu.Lock()
		plans.fail = false
		plans.mu.Unlock()
		for _, b := range []string{created, pastDue, stale} {
			if code, resp := post(hook, b, signed(b, time.Now())); code != 200 {
				t.Fatalf("webhook: %d %s", code, resp)
			}
		}
		plans.mu.Lock()
		reqs := append([]*plansv1.SyncProviderSubscriptionRequest(nil), plans.reqs...)
		plans.mu.Unlock()
		if len(reqs) == 0 || reqs[0].GetSubscriptionId() != qsub.String() || reqs[0].GetProvider() != "paddle" || reqs[0].GetProviderRef() != "sub_01" ||
			reqs[0].GetOwner() != "provider" || reqs[0].GetProviderStatus() != "active" || reqs[0].GetOccurredAt() == nil {
			t.Fatalf("plans-svc was told %v; Paddle runs renewals and retries, so the owner is the provider", reqs)
		}
		code, list := admin("GET", "/admin/tenants/"+tenant.String()+"/provider-subscriptions", "")
		if code != 200 || !strings.Contains(list, `"status":"past_due"`) || !strings.Contains(list, qsub.String()) || strings.Contains(list, `"status":"active"`) {
			t.Fatalf("subscriptions: %d %s; the late-arriving older event must not undo past_due, and our subscription id must survive", code, list)
		}
	})

	t.Run("a refund goes out as an adjustment and its approval is recorded", func(t *testing.T) {
		refund := &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_RefundPayment{RefundPayment: &commandsv1.RefundPayment{InvoiceId: invoice.String(), PspChargeId: "txn_01",
				AmountMinor: 1116, Currency: "USD", IdempotencyKey: invoice.String() + ":refund"}}}
		r, err := h.Handle(ctx, refund)
		if err != nil || r.GetPaymentRefunded().GetPspRefundId() != "adj_01" {
			t.Fatalf("refund = %v, %v", r, err)
		}
		if fake.last["transaction_id"] != "txn_01" || fake.last["action"] != "refund" {
			t.Errorf("adjustment request = %v", fake.last)
		}
		body := event("evt_7", "adjustment.updated", "2027-01-06T10:00:00Z", `{"id":"adj_01","action":"refund","status":"approved","transaction_id":"txn_01","currency_code":"USD","totals":{"total":"1116"}}`)
		if code, resp := post(hook, body, signed(body, time.Now())); code != 200 {
			t.Fatalf("webhook: %d %s", code, resp)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_ledger WHERE tenant_id = $1 AND kind = 'refund' AND provider_ref = 'adj_01'`, tenant).Scan(&n); err != nil || n != 1 {
			t.Fatalf("refund rows = %d, %v; the command's answer and the approval are one fact", n, err)
		}
	})

	t.Run("a checkout is created through the operator API", func(t *testing.T) {
		code, body := admin("POST", "/admin/tenants/"+tenant.String()+"/checkouts?environment=live", `{"provider":"paddle","price_ref":"pri_01","quantity":1,"quotify_subscription_id":"`+uuid.NewString()+`"}`)
		if code != 201 || !strings.Contains(body, "https://pay.example/co") || !strings.Contains(body, "txn_co") {
			t.Fatalf("checkout: %d %s", code, body)
		}
		if code, _ := admin("POST", "/admin/tenants/"+tenant.String()+"/checkouts?environment=live", `{"provider":"paddle"}`); code != 400 {
			t.Errorf("a checkout without a price: %d", code)
		}
		if code, _ := admin("POST", "/admin/tenants/"+tenant.String()+"/checkouts?environment=live", `{"provider":"iyzico","price_ref":"x"}`); code != 404 {
			t.Errorf("a checkout at a provider with no account: %d", code)
		}
	})

	t.Run("webhook URLs that name no real tenant or environment are refused", func(t *testing.T) {
		body := completed("evt_9", key, "txn_09")
		for _, p := range []string{"/webhooks/paddle/not-a-uuid/live", "/webhooks/paddle/" + tenant.String() + "/staging"} {
			if code, _ := post(p, body, signed(body, time.Now())); code != 404 {
				t.Errorf("%s: %d", p, code)
			}
		}
		// A tenant without an account: nothing to verify with, so the provider is told to retry rather than the event being accepted.
		if code, _ := post("/webhooks/paddle/"+uuid.NewString()+"/live", body, signed(body, time.Now())); code != 503 {
			t.Errorf("tenant without an account: %d", code)
		}
	})
}

// A charge that names its provider goes to that account even when another is the tenant's default.
func TestChargeNamesItsProvider(t *testing.T) {
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
	paddleAPI, iyzicoAPI := newFakePaddle(t), newFakeIyzico(t)
	box, _ := secretbox.New(bytes.Repeat([]byte{3}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	paddle.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	tenant := uuid.New()
	if err := accounts.Put(ctx, tenant, "paddle", "live", []byte(`{"api_key":"pdl_secret","base_url":"`+paddleAPI.srv.URL+`","webhook_secret":"s","usage_product_id":"pro_u"}`), false); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Put(ctx, tenant, "iyzico", "live", []byte(`{"api_key":"a","secret_key":"s","base_url":"`+iyzicoAPI.srv.URL+`","buyer":{"name":"A","surname":"B","identity_number":"1","address":"x","city":"c","country":"t","zip_code":"1","ip":"1.1.1.1","email":"b@example.com"}}`), true); err != nil {
		t.Fatal(err)
	}
	h := &handler.Handler{Sandbox: payments.NewMockPSP("http://unused", 0), Attempts: store.Attempts{DB: pool}, Ledger: store.Ledger{DB: pool},
		Resolver: &handler.Resolver{Registry: registry, Accounts: accounts}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	charge := func(provider, method string) *commandsv1.CommandEnvelope {
		inv := uuid.New()
		return &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), TenantId: tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
			Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: inv.String(), AmountMinor: 100, Currency: "USD",
				PaymentMethod: method, Provider: provider, IdempotencyKey: inv.String() + ":charge:0"}}}
	}
	if r, err := h.Handle(ctx, charge("paddle", "sub_01")); err != nil || r.GetPaymentErrored().GetCode() != handler.CodePending {
		t.Fatalf("named paddle: %v, %v", r, err)
	}
	if paddleAPI.count("POST /subscriptions/sub_01/charge") != 1 || iyzicoAPI.count("/payment/auth") != 0 {
		t.Fatalf("paddle calls %d, iyzico calls %d", paddleAPI.count("POST /subscriptions/sub_01/charge"), iyzicoAPI.count("/payment/auth"))
	}
	if r, err := h.Handle(ctx, charge("", "ck:ct")); err != nil || r.GetPaymentSucceeded() == nil {
		t.Fatalf("unnamed goes to the default (iyzico): %v, %v", r, err)
	}
	if r, err := h.Handle(ctx, charge("nope", "x")); err != nil || r.GetPaymentErrored().GetCode() != handler.CodeNotConfigured {
		t.Fatalf("a provider with no account: %v, %v", r, err)
	}
}

func TestProviderMigrationThroughTheWorker(t *testing.T) {
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
	fakeP, fakeI := newFakePaddle(t), newFakeIyzico(t)
	box, _ := secretbox.New(bytes.Repeat([]byte{5}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	paddle.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	plans := &recPlans{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	subs := store.Subscriptions{DB: pool}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: store.Attempts{DB: pool},
		Ledger: store.Ledger{DB: pool}, Subscriptions: subs, Log: log, Token: "internal", Plans: plans}).Handler())
	defer api.Close()

	tenant := uuid.New()
	const secret = "whsec_m"
	if err := accounts.Put(ctx, tenant, "paddle", "live", []byte(`{"api_key":"pdl_secret","base_url":"`+fakeP.srv.URL+`","webhook_secret":"`+secret+`","usage_product_id":"pro_u"}`), false); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Put(ctx, tenant, "iyzico", "live", []byte(`{"api_key":"a","secret_key":"s","base_url":"`+fakeI.srv.URL+`","buyer":{"name":"A","surname":"B","identity_number":"1","address":"x","city":"c","country":"t","zip_code":"1","ip":"1.1.1.1","email":"b@example.com"}}`), true); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, hdr http.Header) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		for k, v := range hdr {
			req.Header[k] = v
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	migrate := func(body string) (int, string) {
		return call("POST", "/admin/tenants/"+tenant.String()+"/migrations?environment=live", body, nil)
	}
	hook := "/webhooks/paddle/" + tenant.String() + "/live"
	webhookCall := func(event string) (int, string) {
		h := http.Header{}
		h.Set("Paddle-Signature", paddle.SignatureHeader(secret, time.Now(), []byte(event)))
		req, _ := http.NewRequest("POST", api.URL+hook, strings.NewReader(event))
		req.Header = h
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	subID := uuid.NewString()
	const migID = "11111111-1111-1111-1111-111111111111"

	t.Run("to a provider that runs subscriptions: a checkout first, the old subscription untouched", func(t *testing.T) {
		plans.sub = &plansv1.Subscription{Id: subID, Provider: "iyzico", Owner: "quotify"}
		code, body := migrate(`{"subscription_id":"` + subID + `","to_provider":"paddle"}`)
		if code != 400 {
			t.Fatalf("without a price: %d %s", code, body)
		}
		code, body = migrate(`{"subscription_id":"` + subID + `","to_provider":"paddle","price_ref":"pri_01"}`)
		if code != 201 || !strings.Contains(body, "https://pay.example/co") || !strings.Contains(body, migID) || !strings.Contains(body, `"state":"started"`) {
			t.Fatalf("migrate: %d %s", code, body)
		}
		if len(plans.started) != 1 || plans.started[0].GetCutoverTime() != nil {
			t.Fatalf("started = %v; no cutover yet", plans.started)
		}
		cd, _ := fakeP.last["custom_data"].(map[string]any)
		if cd["quotify_migration_id"] != migID {
			t.Errorf("the checkout does not carry the migration id: %v", fakeP.last)
		}
	})

	t.Run("a checkout that cannot be made gives the migration up", func(t *testing.T) {
		plans.aborted = nil
		fakeP.mu.Lock()
		before := len(plans.started)
		fakeP.mu.Unlock()
		code, _ := migrate(`{"subscription_id":"` + subID + `","to_provider":"paddle","price_ref":"not-a-price"}`)
		if code != 502 {
			t.Fatalf("status %d", code)
		}
		if len(plans.aborted) != 1 || len(plans.started) != before+1 {
			t.Fatalf("aborted %v, started %d; a migration that cannot get a checkout must not stay open", plans.aborted, len(plans.started))
		}
	})

	t.Run("Paddle's confirmation completes it", func(t *testing.T) {
		plans.completed = nil
		event := `{"event_id":"evt_m1","event_type":"subscription.created","occurred_at":"2027-01-05T12:00:00Z","data":{"id":"sub_NEW","status":"active",
			"custom_data":{"quotify_migration_id":"` + migID + `"},"next_billed_at":"2027-02-05T12:00:00Z"}}`
		if code, body := webhookCall(event); code != 200 {
			t.Fatalf("webhook: %d %s", code, body)
		}
		if len(plans.completed) != 1 || plans.completed[0].GetMigrationId() != migID || plans.completed[0].GetProviderRef() != "sub_NEW" ||
			plans.completed[0].GetCutoverTime().AsTime().Year() != 2027 {
			t.Fatalf("completed = %v", plans.completed)
		}
		q, found, err := subs.QuotifyID(ctx, tenant, "paddle", "live", "sub_NEW")
		if err != nil || !found || q.String() != "22222222-2222-2222-2222-222222222222" {
			t.Fatalf("remembered subscription = %v, %v, %v", q, found, err)
		}
		// Later events of that subscription carry no id of ours; the remembered one is used.
		plans.reqs = nil
		later := `{"event_id":"evt_m2","event_type":"subscription.updated","occurred_at":"2027-01-06T12:00:00Z","data":{"id":"sub_NEW","status":"past_due","custom_data":null}}`
		if code, body := webhookCall(later); code != 200 {
			t.Fatalf("webhook: %d %s", code, body)
		}
		if len(plans.reqs) != 1 || plans.reqs[0].GetSubscriptionId() != "22222222-2222-2222-2222-222222222222" || plans.reqs[0].GetProviderStatus() != "past_due" {
			t.Fatalf("sync = %v", plans.reqs)
		}
	})

	t.Run("the old provider's subscription is cancelled once the new one has taken over", func(t *testing.T) {
		plans.completion = &plansv1.CompleteProviderMigrationResponse{Subscription: &plansv1.Subscription{Id: "33333333-3333-3333-3333-333333333333"},
			SourceProvider: "paddle", SourceProviderRef: "sub_OLD"}
		fakeP.mu.Lock()
		fakeP.cancelBody = nil
		fakeP.mu.Unlock()
		event := `{"event_id":"evt_m3","event_type":"subscription.created","occurred_at":"2027-03-05T12:00:00Z","data":{"id":"sub_N2","status":"active","custom_data":{"quotify_migration_id":"` + migID + `"}}}`
		if code, body := webhookCall(event); code != 200 {
			t.Fatalf("webhook: %d %s", code, body)
		}
		fakeP.mu.Lock()
		defer fakeP.mu.Unlock()
		if fakeP.cancelBody["effective_from"] != "immediately" || fakeP.calls["POST /subscriptions/sub_OLD/cancel"] != 1 {
			t.Fatalf("cancel body %v calls %v; one more renewal at the old provider would be a second bill", fakeP.cancelBody, fakeP.calls)
		}
		plans.completion = nil
	})

	t.Run("if the old provider cannot be told, Paddle's webhook is retried", func(t *testing.T) {
		plans.completion = &plansv1.CompleteProviderMigrationResponse{Subscription: &plansv1.Subscription{Id: "44444444-4444-4444-4444-444444444444"},
			SourceProvider: "paddle", SourceProviderRef: "sub_OLD2"}
		fakeP.mu.Lock()
		fakeP.failCancel = true
		fakeP.mu.Unlock()
		event := `{"event_id":"evt_m4","event_type":"subscription.created","occurred_at":"2027-04-05T12:00:00Z","data":{"id":"sub_N3","status":"active","custom_data":{"quotify_migration_id":"` + migID + `"}}}`
		if code, _ := webhookCall(event); code != 503 {
			t.Fatalf("status %d, want 503 so Paddle retries and the release is attempted again", code)
		}
		fakeP.mu.Lock()
		fakeP.failCancel = false
		fakeP.mu.Unlock()
		if code, body := webhookCall(event); code != 200 {
			t.Fatalf("the retry: %d %s", code, body)
		}
		plans.completion = nil
	})

	t.Run("to a provider Payrudder runs: from a provider-run subscription at the end of the paid period, old provider told first", func(t *testing.T) {
		plans.sub = &plansv1.Subscription{Id: subID, Provider: "paddle", ProviderRef: "sub_PAID", Owner: "provider"}
		// Nothing is known of the paid period yet: nothing is cancelled and nothing is started.
		startedBefore := len(plans.started)
		if code, body := migrate(`{"subscription_id":"` + subID + `","to_provider":"iyzico"}`); code != 409 {
			t.Fatalf("unknown paid period: %d %s", code, body)
		}
		if len(plans.started) != startedBefore {
			t.Fatal("a migration was started without knowing the end of the paid period")
		}
		fakeP.mu.Lock()
		cancelled := fakeP.calls["POST /subscriptions/sub_PAID/cancel"]
		fakeP.mu.Unlock()
		if cancelled != 0 {
			t.Fatal("the old subscription was cancelled although the migration could not be planned")
		}
		next := time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)
		if err := subs.Upsert(ctx, tenant, "paddle", "live", "sub_PAID", nil, "active", &next, time.Now()); err != nil {
			t.Fatal(err)
		}
		// Paddle refuses to cancel: nothing is started either, the customer keeps paying one provider.
		fakeP.mu.Lock()
		fakeP.failCancel = true
		fakeP.mu.Unlock()
		if code, _ := migrate(`{"subscription_id":"` + subID + `","to_provider":"iyzico"}`); code != 502 {
			t.Fatalf("cancel failure: %d", code)
		}
		if len(plans.started) != startedBefore {
			t.Fatal("a migration was started although the old subscription could not be cancelled")
		}
		fakeP.mu.Lock()
		fakeP.failCancel = false
		fakeP.mu.Unlock()
		code, body := migrate(`{"subscription_id":"` + subID + `","to_provider":"iyzico"}`)
		if code != 201 || !strings.Contains(body, `"state":"completed"`) {
			t.Fatalf("migrate: %d %s", code, body)
		}
		fakeP.mu.Lock()
		defer fakeP.mu.Unlock()
		if fakeP.cancelBody["effective_from"] != "next_billing_period" {
			t.Errorf("cancel = %v; the paid period must run out", fakeP.cancelBody)
		}
		last := plans.started[len(plans.started)-1]
		if last.GetToProvider() != "iyzico" || !last.GetCutoverTime().AsTime().Equal(next) {
			t.Errorf("started = %v; the cutover is the end of the paid period", last)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		if code, _ := call("POST", "/admin/tenants/"+tenant.String()+"/migrations?environment=live", `{}`, nil); code != 400 {
			t.Errorf("empty body: %d", code)
		}
		if code, _ := migrate(`{"subscription_id":"` + subID + `","to_provider":"stripe"}`); code != 404 {
			t.Errorf("a provider with no account: %d", code)
		}
		plans.startErr = status.Error(codes.FailedPrecondition, "cannot be moved")
		if code, _ := migrate(`{"subscription_id":"` + subID + `","to_provider":"paddle","price_ref":"pri_01"}`); code != 409 {
			t.Errorf("plans-svc refuses: %d", code)
		}
		plans.startErr = nil
		if code, _ := call("POST", "/admin/tenants/"+tenant.String()+"/migrations/"+migID+"/abort", ``, nil); code != 200 {
			t.Errorf("abort: %d", code)
		}
	})
}
