//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/health"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// faultWorld is a provider that can be told to fail in each of the ways a provider fails, per account, and that records every call it gets: the fault-injecting fake of
// docs/provider-health.md. An account is a row of provider_accounts whose settings name it; the provider is registered once.
type faultWorld struct {
	mu      sync.Mutex
	mode    map[string]string // account -> what its charges do
	check   map[string]error  // account -> what its health check says
	calls   []string          // "account method key", in order
	charges map[string]string // account|key -> charge id, for an account that dedupes by key
	n       int
}

func newFaultWorld() *faultWorld {
	return &faultWorld{mode: map[string]string{}, check: map[string]error{}, charges: map[string]string{}}
}

func (w *faultWorld) set(account, mode string) { w.mu.Lock(); w.mode[account] = mode; w.mu.Unlock() }
func (w *faultWorld) called(account, method string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var keys []string
	for _, c := range w.calls {
		f := strings.Fields(c)
		if f[0] == account && f[1] == method {
			keys = append(keys, f[2])
		}
	}
	return keys
}

type faultProvider struct {
	w          *faultWorld
	account    string
	idempotent bool
}

func (w *faultWorld) factory(settings []byte) (payments.Provider, error) {
	var s struct {
		Account    string `json:"account"`
		Idempotent bool   `json:"idempotent"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil, err
	}
	return &faultProvider{w: w, account: s.Account, idempotent: s.Idempotent}, nil
}

func (p *faultProvider) Name() string { return "fault" }
func (p *faultProvider) Capabilities() payments.Capabilities {
	return payments.Capabilities{Name: "fault", Refunds: true, IdempotentCharges: p.idempotent, IdempotentRefunds: true}
}

func (p *faultProvider) Charge(_ context.Context, r payments.ChargeRequest) (payments.ChargeResult, error) {
	p.w.mu.Lock()
	defer p.w.mu.Unlock()
	p.w.calls = append(p.w.calls, p.account+" charge "+r.IdempotencyKey)
	switch p.w.mode[p.account] {
	case "timeout":
		return payments.ChargeResult{}, payments.ErrTimeout
	case "outage":
		return payments.ChargeResult{}, &payments.TransientError{Err: errors.New("psp answered 503")}
	case "notprocessed":
		return payments.ChargeResult{}, &payments.NotProcessedError{Err: &payments.TransientError{Err: errors.New("psp answered 503 before processing")}}
	case "dial":
		return payments.ChargeResult{}, &payments.TransientError{Err: &url.Error{Op: "Post", URL: "https://psp", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}}
	case "decline":
		return payments.ChargeResult{}, &payments.DeclinedError{Code: "card_declined"}
	}
	if id, ok := p.w.charges[p.account+"|"+r.IdempotencyKey]; ok && p.idempotent {
		return payments.ChargeResult{ChargeID: id}, nil
	}
	p.w.n++
	id := fmt.Sprintf("ch_%s_%d", p.account, p.w.n)
	p.w.charges[p.account+"|"+r.IdempotencyKey] = id
	return payments.ChargeResult{ChargeID: id}, nil
}

func (p *faultProvider) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	return payments.RefundResult{RefundID: "re_1"}, nil
}

func (p *faultProvider) Status(_ context.Context, key string) (payments.StatusResult, error) {
	p.w.mu.Lock()
	defer p.w.mu.Unlock()
	p.w.calls = append(p.w.calls, p.account+" status "+key)
	if id, ok := p.w.charges[p.account+"|"+key]; ok {
		return payments.StatusResult{State: payments.StateSucceeded, ChargeID: id}, nil
	}
	return payments.StatusResult{State: payments.StateNotFound}, nil
}

func (p *faultProvider) CheckHealth(context.Context) error {
	p.w.mu.Lock()
	defer p.w.mu.Unlock()
	return p.w.check[p.account]
}

// failoverRig is a tenant with two accounts of the fault provider ("fault" the default, "fault~b"), a handler with breakers and failover, and the operator API.
type failoverRig struct {
	t        *testing.T
	w        *faultWorld
	world    *world
	tenant   uuid.UUID
	h        *handler.Handler
	breakers *health.Breakers
	health   store.ProviderHealth
	resolver *handler.Resolver
	api      *httptest.Server
	clock    *testClock
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newFailoverRig(t *testing.T, aIdempotent bool) *failoverRig {
	t.Helper()
	pg := testkit.StartPostgres(t)
	wd := openDB(t, pg)
	ctx := context.Background()
	w := newFaultWorld()
	box, _ := secretbox.New(bytes.Repeat([]byte{3}, 32))
	registry := payments.NewRegistry()
	registry.Register("fault", w.factory)
	accounts := store.Accounts{DB: wd.DB, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	r := &failoverRig{t: t, w: w, world: wd, tenant: uuid.New(), resolver: resolver, health: store.ProviderHealth{DB: wd.DB}, clock: &testClock{t: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := accounts.Put(ctx, r.tenant, "fault", "live", []byte(fmt.Sprintf(`{"account":"fault","idempotent":%v}`, aIdempotent)), true); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Put(ctx, r.tenant, "fault~b", "live", []byte(`{"account":"fault~b","idempotent":true}`), false); err != nil {
		t.Fatal(err)
	}
	r.breakers = &health.Breakers{Settings: payments.BreakerSettings{Failures: 3, OpenFor: time.Minute}, Store: r.health, Log: log, Now: r.clock.now}
	r.h = &handler.Handler{Sandbox: payments.NewMockPSP("http://127.0.0.1:1", 0), Attempts: store.Attempts{DB: wd.DB}, Ledger: store.Ledger{DB: wd.DB}, Resolver: resolver, Log: log,
		Breakers: r.breakers, Failovers: r.health}
	srv := &webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Ledger: store.Ledger{DB: wd.DB}, Log: log, Token: "internal"}
	r.api = httptest.NewServer(srv.Handler())
	t.Cleanup(r.api.Close)
	return r
}

func (r *failoverRig) failover(to string) {
	r.t.Helper()
	if err := r.health.SetFailover(context.Background(), r.tenant, "live", "fault", to, "test"); err != nil {
		r.t.Fatal(err)
	}
}

func (r *failoverRig) charge(invoice, command uuid.UUID, key string) (*commandsv1.ReplyEnvelope, error) {
	return r.h.Handle(context.Background(), &commandsv1.CommandEnvelope{CommandId: command.String(), SagaId: uuid.NewString(), TenantId: r.tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
		Command: &commandsv1.CommandEnvelope_ChargePayment{ChargePayment: &commandsv1.ChargePayment{InvoiceId: invoice.String(), AmountMinor: 1116, Currency: "USD",
			PaymentMethod: "pm_card", IdempotencyKey: key}}})
}

func (r *failoverRig) status(invoice uuid.UUID, key string) (*commandsv1.ReplyEnvelope, error) {
	return r.h.Handle(context.Background(), &commandsv1.CommandEnvelope{CommandId: uuid.NewString(), SagaId: uuid.NewString(), TenantId: r.tenant.String(), Environment: plansv1.Environment_ENVIRONMENT_LIVE,
		Command: &commandsv1.CommandEnvelope_QueryPaymentStatus{QueryPaymentStatus: &commandsv1.QueryPaymentStatus{InvoiceId: invoice.String(), IdempotencyKey: key}}})
}

func (r *failoverRig) count(sql string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.world.At(r.tenant).QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *failoverRig) call(method, path, body string) (int, map[string]any) {
	r.t.Helper()
	req, _ := http.NewRequest(method, r.api.URL+"/admin/tenants/"+r.tenant.String()+path, strings.NewReader(body))
	req.Header.Set("X-Internal-Token", "internal")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// A timeout never fails over. The provider may have charged and be slow to say, so a second provider would charge the customer twice: the reply says the outcome is
// unknown (the saga then asks the first provider by the key), a second delivery asks the first provider again, and the second account is not called at all.
func TestATimeoutNeverFailsOver(t *testing.T) {
	r := newFailoverRig(t, false)
	r.failover("fault~b")
	r.w.set("fault", "timeout")
	invoice, cmd, key := uuid.New(), uuid.New(), "inv-timeout:charge:0"

	reply, err := r.charge(invoice, cmd, key)
	if err != nil || reply.GetPaymentErrored().GetCode() != handler.CodeTimeout {
		t.Fatalf("reply = %v, %v; want PaymentErrored TIMEOUT", reply, err)
	}
	if reply, err = r.charge(invoice, cmd, key); err != nil || reply.GetPaymentErrored().GetCode() != handler.CodeTimeout {
		t.Fatalf("second delivery = %v, %v", reply, err)
	}
	if got := r.w.called("fault~b", "charge"); len(got) != 0 {
		t.Fatalf("the second account was charged after a timeout: %v", got)
	}
	if got := r.w.called("fault~b", "status"); len(got) != 0 {
		t.Fatalf("the second account was asked after a timeout: %v", got)
	}
	if a := r.w.called("fault", "charge"); len(a) != 2 || a[0] != key || a[1] != key {
		t.Fatalf("calls to the first account = %v, want the same key twice", a)
	}
	if n := r.count(`SELECT count(*) FROM payment_attempts WHERE tenant_id = $1 AND position('~fo~' in idempotency_key) > 0`, r.tenant); n != 0 {
		t.Errorf("%d failover attempts were started after a timeout", n)
	}
	// A non-definite failure of another kind (a 503 that says nothing about processing) is not a failover either: it is retried.
	r.w.set("fault", "outage")
	if reply, err = r.charge(uuid.New(), uuid.New(), "inv-503:charge:0"); err == nil || reply != nil || !payments.IsOutage(err) {
		t.Fatalf("a 503 = %v, %v; want it returned to be retried", reply, err)
	}
	if got := r.w.called("fault~b", "charge"); len(got) != 0 {
		t.Fatalf("a bare 503 failed over: %v", got)
	}
}

// A failure that is definite (the provider says it did not process the request, or the connection never opened) on a charge that is new fails over to the configured
// account as a new attempt under a new key, which is the same on every delivery. A command delivered again goes on at the second account and never back to the first,
// which may be up again by now; the status query asks the account that took the charge, under the key it was made with.
func TestADefiniteFailureFailsOverUnderANewKey(t *testing.T) {
	for _, mode := range []string{"notprocessed", "dial"} {
		t.Run(mode, func(t *testing.T) {
			r := newFailoverRig(t, false)
			r.failover("fault~b")
			r.w.set("fault", mode)
			ctx := context.Background()
			invoice, cmd, key := uuid.New(), uuid.New(), "inv-fo-"+mode+":charge:0"
			key2 := payments.FailoverKey(key, "fault~b")

			reply, err := r.charge(invoice, cmd, key)
			if err != nil || reply.GetPaymentSucceeded().GetPspChargeId() != "ch_fault~b_1" {
				t.Fatalf("reply = %v, %v; want a success from the second account", reply, err)
			}
			if a, b := r.w.called("fault", "charge"), r.w.called("fault~b", "charge"); len(a) != 1 || a[0] != key || len(b) != 1 || b[0] != key2 || key2 == key {
				t.Fatalf("calls: first %v, second %v; want the original key once, then the failover key %q", a, b, key2)
			}
			row := r.count(`SELECT count(*) FROM payment_ledger WHERE tenant_id = $1 AND provider = 'fault~b' AND idempotency_key = $2 AND status = 'succeeded' AND kind = 'charge'`, r.tenant, key2)
			if row != 1 {
				t.Errorf("%d ledger rows for the charge at the second account, want 1", row)
			}
			if n := r.count(`SELECT count(*) FROM payment_attempts WHERE tenant_id = $1 AND invoice_id = $2 AND kind = 'charge'`, r.tenant, invoice); n != 2 {
				t.Errorf("%d charge attempts, want one per account", n)
			}
			if p, _, _ := (store.Attempts{DB: r.world.DB}).ChargeProvider(ctx, r.tenant, invoice); p != "fault~b" {
				t.Errorf("the invoice's charge provider = %q; refunds and status queries must go to the account that took the charge", p)
			}

			// Delivered again, with the first account up: it goes on at the second, under the same key, and charges nobody twice.
			r.w.set("fault", "ok")
			reply, err = r.charge(invoice, cmd, key)
			if err != nil || reply.GetPaymentSucceeded().GetPspChargeId() != "ch_fault~b_1" {
				t.Fatalf("second delivery = %v, %v", reply, err)
			}
			if a := r.w.called("fault", "charge"); len(a) != 1 {
				t.Fatalf("the first account was charged again on a second delivery: %v", a)
			}
			if b := r.w.called("fault~b", "charge"); len(b) != 2 || b[1] != key2 {
				t.Fatalf("second account calls = %v, want the failover key again", b)
			}

			// The saga asks about the key it knows.
			st, err := r.status(invoice, key)
			if err != nil || st.GetPaymentStatus().GetState() != commandsv1.PaymentStatus_STATE_SUCCEEDED || st.GetPaymentStatus().GetPspChargeId() != "ch_fault~b_1" {
				t.Fatalf("status = %v, %v", st, err)
			}
			if got := r.w.called("fault~b", "status"); len(got) != 1 || got[0] != key2 {
				t.Errorf("status asked %v at the second account, want the failover key", got)
			}
			if got := r.w.called("fault", "status"); len(got) != 0 {
				t.Errorf("status asked at the first account: %v", got)
			}
		})
	}
}

// Failover happens only for a charge that is new, and only when it is configured. Nothing set: the failure is returned to be retried. A delivery that is not the first
// might have been sent to the first account, so it is never failed over, even on a definite failure.
func TestFailoverIsOnlyForANewChargeAndOnlyWhenConfigured(t *testing.T) {
	r := newFailoverRig(t, false)
	r.w.set("fault", "notprocessed")
	if reply, err := r.charge(uuid.New(), uuid.New(), "inv-none:charge:0"); err == nil || reply != nil || !payments.NotProcessed(err) {
		t.Fatalf("no failover configured: %v, %v; want the failure returned", reply, err)
	}
	if got := r.w.called("fault~b", "charge"); len(got) != 0 {
		t.Fatalf("failed over with nothing configured: %v", got)
	}

	r.failover("fault~b")
	invoice, cmd, key := uuid.New(), uuid.New(), "inv-redelivered:charge:0"
	r.w.set("fault", "outage") // the first delivery: an answer that says nothing about processing, so it may have been sent
	if _, err := r.charge(invoice, cmd, key); err == nil {
		t.Fatal("expected the outage to be returned")
	}
	r.w.set("fault", "notprocessed") // the second delivery fails definitely, but the first might have reached the provider
	if reply, err := r.charge(invoice, cmd, key); err == nil || reply != nil {
		t.Fatalf("second delivery = %v, %v; want the failure returned, not a failover", reply, err)
	}
	if got := r.w.called("fault~b", "charge"); len(got) != 0 {
		t.Fatalf("a delivery that was not the first failed over: %v", got)
	}

	// A decline is the provider's answer; it is not a failure to fail over from. (The outages above opened the circuit; after its cool-down this charge is the probe,
	// and a decline shows the provider is answering.)
	r.clock.add(time.Minute)
	r.w.set("fault", "decline")
	if reply, err := r.charge(uuid.New(), uuid.New(), "inv-decline:charge:0"); err != nil || reply.GetPaymentDeclined() == nil {
		t.Fatalf("decline = %v, %v", reply, err)
	}
	if got := r.w.called("fault~b", "charge"); len(got) != 0 {
		t.Fatalf("a decline failed over: %v", got)
	}
}

// The circuit breaker opens after the configured outages in a row and then charges fail fast: the provider is not called and the error is the retryable circuit-open
// error. Its state is kept where the integrations page can read it. After the cool-down one charge is the probe: a good one closes the circuit. With a failover set, a
// charge that finds the circuit open goes to the other account (the first was not called), still as a new attempt under a new key.
func TestTheCircuitBreakerFailsNewChargesFastAndRecovers(t *testing.T) {
	r := newFailoverRig(t, true)
	ctx := context.Background()
	r.w.set("fault", "outage")
	for i := 0; i < 3; i++ {
		if _, err := r.charge(uuid.New(), uuid.New(), fmt.Sprintf("inv-b%d:charge:0", i)); !payments.IsOutage(err) {
			t.Fatalf("charge %d: %v", i, err)
		}
	}
	if got := len(r.w.called("fault", "charge")); got != 3 {
		t.Fatalf("%d calls to the provider before the circuit opened", got)
	}
	_, err := r.charge(uuid.New(), uuid.New(), "inv-b3:charge:0")
	var tr *payments.TransientError
	if !errors.Is(err, payments.ErrCircuitOpen) || !errors.As(err, &tr) || tr.RetryAfter <= 0 {
		t.Fatalf("open circuit: %v, want a retryable circuit-open error", err)
	}
	if got := len(r.w.called("fault", "charge")); got != 3 {
		t.Fatalf("the provider was called with the circuit open (%d calls)", got)
	}
	_, hs := r.call("GET", "/account-health", "")
	if got := accountOf(hs, "fault"); got["breaker"] != "open" || got["status"] != "down" || got["consecutive_failures"] != float64(3) || got["opened_at"] == nil {
		t.Fatalf("health of the account = %v", got)
	}

	// With a failover set the charge goes to the other account without the first being asked.
	r.failover("fault~b")
	key := "inv-b4:charge:0"
	reply, err := r.charge(uuid.New(), uuid.New(), key)
	if err != nil || reply.GetPaymentSucceeded().GetPspChargeId() != "ch_fault~b_1" {
		t.Fatalf("with an open circuit and a failover: %v, %v", reply, err)
	}
	if got := len(r.w.called("fault", "charge")); got != 3 {
		t.Fatalf("the first account was called with its circuit open (%d calls)", got)
	}
	if b := r.w.called("fault~b", "charge"); len(b) != 1 || b[0] != payments.FailoverKey(key, "fault~b") {
		t.Fatalf("second account calls = %v", b)
	}

	// After the cool-down one charge probes; the provider is back and the circuit closes.
	r.clock.add(time.Minute)
	r.w.set("fault", "ok")
	if reply, err := r.charge(uuid.New(), uuid.New(), "inv-b5:charge:0"); err != nil || reply.GetPaymentSucceeded() == nil {
		t.Fatalf("probe: %v, %v", reply, err)
	}
	if r.breakers.For(health.Ref{Tenant: r.tenant, Provider: "fault", Environment: "live"}).State() != payments.BreakerClosed {
		t.Error("the circuit did not close after a good probe")
	}
	_, hs = r.call("GET", "/account-health", "")
	if got := accountOf(hs, "fault"); got["breaker"] != "closed" || got["status"] != "healthy" {
		t.Errorf("health of the account after the probe = %v", got)
	}
	_ = ctx
}

func accountOf(m map[string]any, provider string) map[string]any {
	list, _ := m["accounts"].([]any)
	for _, a := range list {
		if a, ok := a.(map[string]any); ok && a["provider"] == provider {
			return a
		}
	}
	return nil
}

// The monitor checks every account with the adapter's own health call and keeps what it finds: healthy, down (and the breaker hears of it), credentials refused (the
// provider answered, so not an outage). The state and the failover are readable by the operator API, which also checks what it is given.
func TestTheMonitorKeepsHealthAndTheAPIKeepsTheFailover(t *testing.T) {
	r := newFailoverRig(t, true)
	ctx := context.Background()
	mon := &health.Monitor{Store: r.health, Resolver: r.resolver, Breakers: r.breakers, Interval: time.Minute, Timeout: time.Second,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: r.clock.now}

	r.w.check["fault"], r.w.check["fault~b"] = nil, fmt.Errorf("%w: connection refused", payments.ErrUnreachable)
	mon.Pass(ctx)
	_, hs := r.call("GET", "/account-health", "")
	a, b := accountOf(hs, "fault"), accountOf(hs, "fault~b")
	if a["status"] != "healthy" || a["last_ok_at"] == nil || a["last_checked_at"] == nil || a["is_default"] != true {
		t.Errorf("account a = %v", a)
	}
	if b["status"] != "down" || b["last_error"] != "unreachable" || b["last_failure_at"] == nil {
		t.Errorf("account b = %v", b)
	}
	// The gauge the ProviderAccountDown alert reads: one series per live account.
	if v, ok := gauged(t, "provider_account_up", attribute.String("account", r.tenant.String()+"/fault")); !ok || v != 1 {
		t.Errorf("provider_account_up of the healthy account = %d (present %v), want 1", v, ok)
	}
	if v, ok := gauged(t, "provider_account_up", attribute.String("account", r.tenant.String()+"/fault~b"), attribute.String("provider", "fault")); !ok || v != 0 {
		t.Errorf("provider_account_up of the unreachable account = %d (present %v), want 0", v, ok)
	}
	// Three checks that find the provider down open its circuit without a charge having to fail first.
	mon.Pass(ctx)
	mon.Pass(ctx)
	if got := r.breakers.For(health.Ref{Tenant: r.tenant, Provider: "fault~b", Environment: "live"}).State(); got != payments.BreakerOpen {
		t.Errorf("breaker of b = %s after three failed checks, want open", got)
	}
	r.w.check["fault~b"] = payments.ErrBadCredentials // the provider answers and refuses the key: it is up
	mon.Pass(ctx)
	_, hs = r.call("GET", "/account-health", "")
	if b := accountOf(hs, "fault~b"); b["status"] != "bad_credentials" || b["breaker"] != "closed" {
		t.Errorf("account b after the credentials were refused = %v", b)
	}

	// Failover configuration.
	put := func(provider, body string) (int, map[string]any) {
		return r.call("PUT", "/provider-accounts/"+provider+"/failover?environment=live", body)
	}
	for name, c := range map[string]struct {
		provider, body, key string
		code                int
	}{
		"itself":                       {"fault", `{"failover_to":"fault"}`, "payments.failover.bad_target", 400},
		"a bank transfer":              {"fault", `{"failover_to":"banktransfer"}`, "payments.failover.bad_target", 400},
		"not an account key":           {"fault", `{"failover_to":"fault~"}`, "payments.failover.bad_target", 400},
		"an account that is not there": {"fault", `{"failover_to":"fault~nope"}`, "payments.account.not_found", 404},
	} {
		if code, m := put(c.provider, c.body); code != c.code || errorKey(m) != c.key {
			t.Errorf("%s: %d %v", name, code, m)
		}
	}
	if code, m := put("fault", `{"failover_to":"fault~b"}`); code != 200 || m["failover_to"] != "fault~b" {
		t.Fatalf("put: %d %v", code, m)
	}
	_, hs = r.call("GET", "/account-health", "")
	if a := accountOf(hs, "fault"); a["failover_to"] != "fault~b" {
		t.Errorf("account a = %v", a)
	}
	if code, _ := r.call("DELETE", "/provider-accounts/fault/failover?environment=live", ""); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code, m := r.call("DELETE", "/provider-accounts/fault/failover?environment=live", ""); code != 404 || errorKey(m) != "payments.failover.not_found" {
		t.Errorf("delete again: %d %v", code, m)
	}
	// Setting and removing the failover is in the audit log (pkg/audit), once each: the refused and the second delete are not.
	logged, err := r.world.CountAll(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id = $1 AND action IN ('provider_account.failover_set', 'provider_account.failover_cleared')
		AND target_id = 'fault/live'`, r.tenant)
	if err != nil || logged != 2 {
		t.Errorf("failover entries in the audit log = %d, %v; want 2", logged, err)
	}
}
