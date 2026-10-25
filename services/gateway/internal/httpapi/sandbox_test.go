package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/test/bufconn"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

type simPlans struct {
	plansv1.UnimplementedPlansServiceServer
	mu      sync.Mutex
	methods map[string]string // external id -> payment method
}

func (p *simPlans) SetPaymentMethod(_ context.Context, req *plansv1.SetPaymentMethodRequest) (*plansv1.SetPaymentMethodResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.methods[req.GetExternalId()] = req.GetPaymentMethod()
	return &plansv1.SetPaymentMethodResponse{Customer: &plansv1.Customer{ExternalId: req.GetExternalId(), PaymentMethod: req.GetPaymentMethod()}}, nil
}

type simBilling struct {
	billingv1.UnimplementedBillingServiceServer
	billingv1.UnimplementedBillingAdminServiceServer
	mu       sync.Mutex
	retried  []*billingv1.RetryInvoiceSagaRequest
	operator bool
	retryErr error
}

func (b *simBilling) GetInvoice(_ context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.GetInvoiceResponse, error) {
	if req.GetInvoiceId() != "inv-1" {
		return nil, errkey.New(codes.NotFound, "billing.invoice.not_found", "no such invoice")
	}
	return &billingv1.GetInvoiceResponse{Invoice: &billingv1.Invoice{Id: "inv-1", CustomerExternalId: "cus_1", Status: "open"}}, nil
}

func (b *simBilling) RetryInvoiceSaga(ctx context.Context, req *billingv1.RetryInvoiceSagaRequest) (*billingv1.RetryInvoiceSagaResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.retried = append(b.retried, req)
	b.operator = tenant.IsOperator(ctx)
	if b.retryErr != nil {
		return nil, b.retryErr
	}
	return &billingv1.RetryInvoiceSagaResponse{}, nil
}

func newSimServer(t *testing.T) (*httptest.Server, *simPlans, *simBilling) {
	t.Helper()
	pl, bl := &simPlans{methods: map[string]string{}}, &simBilling{}
	gs, _ := grpcx.NewServer(grpcx.ServerOptions{Extra: []grpc.ServerOption{grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor())}})
	plansv1.RegisterPlansServiceServer(gs, pl)
	billingv1.RegisterBillingServiceServer(gs, bl)
	billingv1.RegisterBillingAdminServiceServer(gs, bl)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cc, err := grpcx.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	ts := httptest.NewServer(New(Deps{Plans: plansv1.NewPlansServiceClient(cc), Billing: billingv1.NewBillingServiceClient(cc), BillingAdmin: billingv1.NewBillingAdminServiceClient(cc),
		Auth: fakeAuth{}, Limiter: &fakeLimiter{}, OperatorToken: operatorToken, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	t.Cleanup(ts.Close)
	return ts, pl, bl
}

func TestScenarioCatalogueAndSimulate(t *testing.T) {
	ts, pl, bl := newSimServer(t)

	// The catalogue is the six pm_sandbox_* tokens, one default; sandbox only.
	resp, m := do(t, ts, "GET", "/v1/sandbox/scenarios", "sandbox-key", "")
	list, _ := m["scenarios"].([]any)
	if resp.StatusCode != 200 || len(list) != 6 {
		t.Fatalf("scenarios: %d %v", resp.StatusCode, m)
	}
	defaults := 0
	for _, raw := range list {
		sc := raw.(map[string]any)
		if !strings.HasPrefix(sc["payment_method"].(string), "pm_sandbox_") || sc["outcome"] == "" || sc["description"] == "" {
			t.Errorf("scenario %v", sc)
		}
		if sc["default"] == true {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("%d default scenarios", defaults)
	}
	if resp, _ := do(t, ts, "GET", "/v1/sandbox/scenarios", "live-key", ""); resp.StatusCode != 403 {
		t.Errorf("a live key reads the sandbox catalogue: %d", resp.StatusCode)
	}

	// Simulate: the customer takes the scenario, the invoice's payment runs again, as an operator call about this tenant only.
	resp, m = do(t, ts, "POST", "/v1/sandbox/invoices/inv-1/simulate", "sandbox-key", `{"scenario":"declined"}`)
	if resp.StatusCode != 200 || m["retried"] != true || m["payment_method"] != "pm_sandbox_declined" || m["customer_external_id"] != "cus_1" {
		t.Fatalf("simulate: %d %v", resp.StatusCode, m)
	}
	if pl.methods["cus_1"] != "pm_sandbox_declined" {
		t.Errorf("customer payment method = %q", pl.methods["cus_1"])
	}
	if len(bl.retried) != 1 || bl.retried[0].GetInvoiceId() != "inv-1" || bl.retried[0].GetTenantId() != sandboxTenant.String() || !bl.operator {
		t.Errorf("retry = %v operator %v: it must be about the key's own tenant", bl.retried, bl.operator)
	}
	// The full token is accepted too.
	if resp, _ := do(t, ts, "POST", "/v1/sandbox/invoices/inv-1/simulate", "sandbox-key", `{"scenario":"pm_sandbox_success"}`); resp.StatusCode != 200 || pl.methods["cus_1"] != "pm_sandbox_success" {
		t.Errorf("full token: %d %q", resp.StatusCode, pl.methods["cus_1"])
	}
	// When the payment cannot run again the scenario is still set and the answer says why.
	bl.retryErr = errkey.New(codes.FailedPrecondition, "billing.saga.event_not_possible", "paid")
	resp, m = do(t, ts, "POST", "/v1/sandbox/invoices/inv-1/simulate", "sandbox-key", `{"scenario":"timeout_committed"}`)
	if resp.StatusCode != 200 || m["retried"] != false || m["retry_error"] != "billing.saga.event_not_possible" || pl.methods["cus_1"] != "pm_sandbox_timeout_committed" {
		t.Errorf("not retryable: %d %v", resp.StatusCode, m)
	}
	// Refusals.
	resp, m = do(t, ts, "POST", "/v1/sandbox/invoices/inv-1/simulate", "sandbox-key", `{"scenario":"nonsense"}`)
	if resp.StatusCode != 400 || errKey(m) != "billing.sandbox.unknown_scenario" {
		t.Errorf("unknown scenario: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "POST", "/v1/sandbox/invoices/other/simulate", "sandbox-key", `{"scenario":"declined"}`); resp.StatusCode != 404 {
		t.Errorf("an invoice that is not the tenant's: %d", resp.StatusCode)
	}
	if resp, _ := do(t, ts, "POST", "/v1/sandbox/invoices/inv-1/simulate", "live-key", `{"scenario":"declined"}`); resp.StatusCode != 403 {
		t.Errorf("a live key simulates: %d", resp.StatusCode)
	}
	if n := len(bl.retried); n != 3 {
		t.Errorf("retries = %d, want only the three accepted requests", n)
	}
}

func TestSandboxResetRoutes(t *testing.T) {
	p := newProduct(t, nil, nil)
	if resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/sandbox/reset", tokAda, "", ""); resp.StatusCode != 202 || m["sandbox_reset"] == nil {
		t.Fatalf("reset: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/sandbox/reset", tokAda, "", ""); resp.StatusCode != 200 {
		t.Fatalf("status: %d %v", resp.StatusCode, m)
	}
	if got := p.ident.resets[0]; got.GetUserId() != adaUser || got.GetAppId() != adaApp {
		t.Errorf("request = %v: the user comes from the session and the app from the path", got)
	}
	if resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/sandbox/reset", tokViewer, "", ""); resp.StatusCode != 403 || errKeyOf(m) != "identity.account.forbidden" {
		t.Errorf("a viewer: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+bobApp+"/sandbox/reset", tokAda, "", ""); resp.StatusCode != 404 {
		t.Errorf("another account's app: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/sandbox/reset", "", "", ""); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
}
