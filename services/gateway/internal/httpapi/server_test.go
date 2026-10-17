package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/gateway/internal/auth"
	"github.com/0furkancolak/quotify/services/gateway/internal/ratelimit"
)

const operatorToken = "op-token-0123456789abcdef0123456789abcdef"

var (
	liveTenant    = uuid.MustParse("0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e")
	sandboxTenant = uuid.MustParse("0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5f")
)

type fakeAuth struct{}

func (fakeAuth) Authenticate(_ context.Context, key string) (auth.Principal, error) {
	switch key {
	case "live-key":
		return auth.Principal{Tenant: tenant.Info{ID: liveTenant, Environment: "live"}, Tier: "free"}, nil
	case "sandbox-key":
		return auth.Principal{Tenant: tenant.Info{ID: sandboxTenant, Environment: "sandbox"}, Tier: "free"}, nil
	case "limited-key":
		return auth.Principal{Tenant: tenant.Info{ID: uuid.New(), Environment: "live"}, Tier: "limited"}, nil
	case "down-key":
		return auth.Principal{}, auth.ErrUnavailable
	default:
		return auth.Principal{}, auth.ErrUnauthenticated
	}
}

type fakeLimiter struct{}

func (fakeLimiter) Allow(_ context.Context, _, tier string, _ ratelimit.Class, _ int) (ratelimit.Decision, error) {
	if tier == "limited" {
		return ratelimit.Decision{Allowed: false, RetryAfter: 1500 * time.Millisecond}, nil
	}
	return ratelimit.Decision{Allowed: true}, nil
}

// fakePlans records the tenant it saw and answers a few RPCs.
type fakePlans struct {
	plansv1.UnimplementedPlansServiceServer
	plansv1.UnimplementedAdminServiceServer
	lastTenant   tenant.Info
	lastOperator bool
	lastPM       string
}

func (f *fakePlans) CreateCustomer(ctx context.Context, req *plansv1.CreateCustomerRequest) (*plansv1.CreateCustomerResponse, error) {
	f.lastTenant, _ = tenant.FromContext(ctx)
	if req.GetEmail() == "bad" {
		st, _ := status.New(codes.InvalidArgument, "invalid request: email: must be a plain e-mail address").WithDetails(
			&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "email", Description: "must be a plain e-mail address"}}})
		return nil, st.Err()
	}
	return &plansv1.CreateCustomerResponse{Customer: &plansv1.Customer{Id: "c1", ExternalId: req.GetExternalId(), Email: req.GetEmail()}}, nil
}

func (f *fakePlans) GetCustomer(context.Context, *plansv1.GetCustomerRequest) (*plansv1.GetCustomerResponse, error) {
	return nil, status.Error(codes.Internal, "pq: secret internals")
}

func (f *fakePlans) SetPaymentMethod(ctx context.Context, req *plansv1.SetPaymentMethodRequest) (*plansv1.SetPaymentMethodResponse, error) {
	f.lastPM = req.GetPaymentMethod()
	return &plansv1.SetPaymentMethodResponse{Customer: &plansv1.Customer{ExternalId: req.GetExternalId(), PaymentMethod: req.GetPaymentMethod()}}, nil
}

func (f *fakePlans) CreateAccount(ctx context.Context, req *plansv1.CreateAccountRequest) (*plansv1.CreateAccountResponse, error) {
	f.lastOperator = tenant.IsOperator(ctx)
	return &plansv1.CreateAccountResponse{Account: &plansv1.Account{Id: "a1", Name: req.GetName()}}, nil
}

func newTestServer(t *testing.T) (*httptest.Server, *fakePlans) {
	t.Helper()
	f := &fakePlans{}
	gs, _ := grpcx.NewServer(grpcx.ServerOptions{Extra: []grpc.ServerOption{grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor())}})
	plansv1.RegisterPlansServiceServer(gs, f)
	plansv1.RegisterAdminServiceServer(gs, f)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cc, err := grpcx.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	api := New(Deps{Plans: plansv1.NewPlansServiceClient(cc), Admin: plansv1.NewAdminServiceClient(cc), Auth: fakeAuth{},
		Limiter: fakeLimiter{}, OperatorToken: operatorToken, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	return ts, f
}

func do(t *testing.T, ts *httptest.Server, method, path, key, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestAuthAndTenantPropagation(t *testing.T) {
	ts, f := newTestServer(t)
	resp, m := do(t, ts, "POST", "/v1/customers", "", `{}`)
	if resp.StatusCode != 401 || errCode(m) != "UNAUTHENTICATED" {
		t.Fatalf("no key: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "POST", "/v1/customers", "nope", `{}`); resp.StatusCode != 401 {
		t.Fatalf("bad key: %d", resp.StatusCode)
	}
	if resp, m := do(t, ts, "POST", "/v1/customers", "down-key", `{}`); resp.StatusCode != 503 || errCode(m) != "UNAVAILABLE" {
		t.Fatalf("auth unavailable: %d %v", resp.StatusCode, m)
	}

	resp, m = do(t, ts, "POST", "/v1/customers", "live-key", `{"external_id":"cus_1","name":"Ada","email":"a@x.io"}`)
	if resp.StatusCode != 201 || m["external_id"] != "cus_1" || resp.Header.Get("X-Request-Id") == "" {
		t.Fatalf("create: %d %v", resp.StatusCode, m)
	}
	if f.lastTenant.ID != liveTenant || f.lastTenant.Environment != "live" {
		t.Fatalf("plans-svc saw tenant %+v", f.lastTenant)
	}
}

func TestErrorMapping(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "POST", "/v1/customers", "live-key", `{"external_id":"c","name":"n","email":"bad"}`)
	details, _ := m["error"].(map[string]any)["details"].([]any)
	if resp.StatusCode != 400 || len(details) != 1 || details[0].(map[string]any)["field"] != "email" {
		t.Fatalf("validation: %d %v", resp.StatusCode, m)
	}
	resp, m = do(t, ts, "GET", "/v1/customers/c", "live-key", "")
	if resp.StatusCode != 500 || m["error"].(map[string]any)["message"] != "internal error" {
		t.Fatalf("internal error leaked: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/v1/customers", "live-key", `{"unknown_field":1}`); resp.StatusCode != 400 || errCode(m) != "INVALID_ARGUMENT" {
		t.Fatalf("unknown field: %d %v", resp.StatusCode, m)
	}
	big := `{"name":"` + strings.Repeat("x", MaxBodyBytes) + `"}`
	if resp, m := do(t, ts, "POST", "/v1/customers", "live-key", big); resp.StatusCode != 413 || errCode(m) != "REQUEST_TOO_LARGE" {
		t.Fatalf("large body: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "GET", "/v1/nope", "live-key", ""); resp.StatusCode != 404 || errCode(m) != "NOT_FOUND" {
		t.Fatalf("unknown route: %d %v", resp.StatusCode, m)
	}
}

func TestRateLimited(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "POST", "/v1/customers", "limited-key", `{}`)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "2" || errCode(m) != "RESOURCE_EXHAUSTED" {
		t.Fatalf("limited: %d retry=%q %v", resp.StatusCode, resp.Header.Get("Retry-After"), m)
	}
}

func TestSandboxRoutes(t *testing.T) {
	ts, f := newTestServer(t)
	if resp, _ := do(t, ts, "POST", "/v1/sandbox/customers/c1/payment-scenario", "live-key", `{"scenario":"declined"}`); resp.StatusCode != 403 {
		t.Fatalf("live key on sandbox route: %d", resp.StatusCode)
	}
	resp, m := do(t, ts, "POST", "/v1/sandbox/customers/c1/payment-scenario", "sandbox-key", `{"scenario":"declined"}`)
	if resp.StatusCode != 200 || f.lastPM != "pm_sandbox_declined" || m["payment_method"] != "pm_sandbox_declined" {
		t.Fatalf("sandbox scenario: %d %v (%s)", resp.StatusCode, m, f.lastPM)
	}
}

func TestOperatorRoutes(t *testing.T) {
	ts, f := newTestServer(t)
	if resp, _ := do(t, ts, "POST", "/admin/v1/accounts", "live-key", `{"name":"x"}`); resp.StatusCode != 401 {
		t.Fatalf("tenant key on admin route: %d", resp.StatusCode)
	}
	resp, m := do(t, ts, "POST", "/admin/v1/accounts", operatorToken, `{"name":"Acme"}`)
	if resp.StatusCode != 201 || m["name"] != "Acme" || !f.lastOperator {
		t.Fatalf("operator create: %d %v operator=%v", resp.StatusCode, m, f.lastOperator)
	}
}
