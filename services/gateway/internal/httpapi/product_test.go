package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	limitsv1 "github.com/0furkancolak/quotify/gen/go/quotify/limits/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/grpcx"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// The product API: sessions, the person's own accounts and apps, and a session acting on a tenant. The fake identity service knows two accounts
// (Ada's and Bob's) and answers like the real one: another account's objects are "not found".

const (
	tokAda    = "qs_ada"
	tokBob    = "qs_bob"
	tokViewer = "qs_viewer" // a viewer of Ada's account
	// Phase 42: sessions of people the operator list may name (see signin_methods_test.go).
	tokOperator           = "qs_operator"            // boss@example.com, verified: an operator
	tokOperatorUnverified = "qs_operator_unverified" // the same address, never verified: not an operator
	tokSpoof              = "qs_spoof"               // an address that only starts like a listed one
	opUser                = "44444444-4444-4444-8444-444444444444"
	opUser2               = "55555555-5555-4555-8555-555555555555"
	spoofUser             = "66666666-6666-4666-8666-666666666666"
	adaUser               = "11111111-1111-4111-8111-111111111111"
	bobUser               = "22222222-2222-4222-8222-222222222222"
	viewerUser            = "33333333-3333-4333-8333-333333333333"
	adaAcct               = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	bobAcct               = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	adaApp                = "a0000000-0000-4000-8000-000000000001"
	bobApp                = "b0000000-0000-4000-8000-000000000001"
	adaLive               = "a1000000-0000-4000-8000-000000000001"
	adaSandbox            = "a1000000-0000-4000-8000-000000000002"
	bobLive               = "b1000000-0000-4000-8000-000000000001"
	bobSandbox            = "b1000000-0000-4000-8000-000000000002"
)

type fakeIdentity struct {
	plansv1.UnimplementedIdentityServiceServer
	mu       sync.Mutex
	revoked  map[string]bool
	lastUser string // the user id the last account-scoped call carried
	keys     map[string]string
	signIns  int

	mem            memberCalls
	signUpReqs     []*plansv1.SignUpRequest
	resetReqs      []*plansv1.RequestPasswordResetRequest
	resetPasswords []*plansv1.ResetPasswordRequest
	resends        int

	// Phase 42: what the one-time code and provider routes passed on.
	codeRequests []*plansv1.RequestLoginCodeRequest
	codeVerifies []*plansv1.VerifyLoginCodeRequest
	identities   []*plansv1.SignInWithIdentityRequest
}

const (
	goodVerifyToken = "qv_good"
	goodResetToken  = "qr_good"
)

func newFakeIdentity() *fakeIdentity {
	return &fakeIdentity{revoked: map[string]bool{}, keys: map[string]string{"key-ada": adaApp, "key-bob": bobApp}}
}

type fakeUser struct {
	id, email string
	verified  bool
}

var tokenUsers = map[string]fakeUser{tokAda: {adaUser, "ada@example.com", true}, tokBob: {bobUser, "bob@example.com", true}, tokViewer: {viewerUser, "view@example.com", true},
	tokOperator: {opUser, "boss@example.com", true}, tokOperatorUnverified: {opUser2, "boss@example.com", false}, tokSpoof: {spoofUser, "ada@example.com.evil.test", true}}

func (f *fakeIdentity) memberships(user string) []*plansv1.Membership {
	switch user {
	case adaUser:
		return []*plansv1.Membership{{AccountId: adaAcct, AccountName: "Ada Labs", Role: "owner"}}
	case bobUser:
		return []*plansv1.Membership{{AccountId: bobAcct, AccountName: "Bob Inc", Role: "owner"}}
	case viewerUser:
		return []*plansv1.Membership{{AccountId: adaAcct, AccountName: "Ada Labs", Role: "viewer"}}
	}
	return nil
}

func (f *fakeIdentity) role(user, account string) string {
	for _, m := range f.memberships(user) {
		if m.GetAccountId() == account {
			return m.GetRole()
		}
	}
	return ""
}

func appFor(id string) *plansv1.App {
	switch id {
	case adaApp:
		return &plansv1.App{Id: adaApp, AccountId: adaAcct, Name: "Ada app", Slug: "ada-app", Tenants: []*plansv1.Tenant{
			{Id: adaLive, AccountId: adaAcct, AppId: adaApp, Environment: plansv1.Environment_ENVIRONMENT_LIVE, Currency: "USD", Timezone: "UTC", Tier: plansv1.TenantTier_TENANT_TIER_FREE},
			{Id: adaSandbox, AccountId: adaAcct, AppId: adaApp, Environment: plansv1.Environment_ENVIRONMENT_SANDBOX, Currency: "USD", Timezone: "UTC", Tier: plansv1.TenantTier_TENANT_TIER_FREE}}}
	case bobApp:
		return &plansv1.App{Id: bobApp, AccountId: bobAcct, Name: "Bob app", Slug: "bob-app", Tenants: []*plansv1.Tenant{
			{Id: bobLive, AccountId: bobAcct, AppId: bobApp, Environment: plansv1.Environment_ENVIRONMENT_LIVE, Currency: "EUR", Timezone: "UTC", Tier: plansv1.TenantTier_TENANT_TIER_FREE},
			{Id: bobSandbox, AccountId: bobAcct, AppId: bobApp, Environment: plansv1.Environment_ENVIRONMENT_SANDBOX, Currency: "EUR", Timezone: "UTC", Tier: plansv1.TenantTier_TENANT_TIER_FREE}}}
	}
	return nil
}

func notFound(what string) error {
	return errkey.New(codes.NotFound, "identity."+what+".not_found", "no such "+what)
}

func (f *fakeIdentity) SignIn(_ context.Context, req *plansv1.SignInRequest) (*plansv1.SignInResponse, error) {
	f.mu.Lock()
	f.signIns++
	f.mu.Unlock()
	if req.GetPassword() != "right password!" {
		return nil, errkey.New(codes.Unauthenticated, "identity.auth.invalid_credentials", "wrong")
	}
	return &plansv1.SignInResponse{User: &plansv1.User{Id: adaUser, Email: req.GetEmail()}, Token: tokAda, Memberships: f.memberships(adaUser), ExpireTime: timestamppb.Now()}, nil
}

func (f *fakeIdentity) SignUp(_ context.Context, req *plansv1.SignUpRequest) (*plansv1.SignUpResponse, error) {
	f.mu.Lock()
	f.signUpReqs = append(f.signUpReqs, req)
	f.mu.Unlock()
	return &plansv1.SignUpResponse{}, nil
}

func (f *fakeIdentity) VerifyEmail(_ context.Context, req *plansv1.VerifyEmailRequest) (*plansv1.VerifyEmailResponse, error) {
	if req.GetToken() != goodVerifyToken {
		return nil, errkey.New(codes.InvalidArgument, "identity.token.invalid", "bad link")
	}
	return &plansv1.VerifyEmailResponse{User: &plansv1.User{Id: adaUser, Email: "ada@example.com", EmailVerified: true}}, nil
}

func (f *fakeIdentity) ResendVerification(_ context.Context, req *plansv1.ResendVerificationRequest) (*plansv1.ResendVerificationResponse, error) {
	f.seen(req.GetUserId())
	f.mu.Lock()
	f.resends++
	n := f.resends
	f.mu.Unlock()
	if n > 1 {
		return nil, errkey.New(codes.ResourceExhausted, "identity.verify.throttled", "wait")
	}
	return &plansv1.ResendVerificationResponse{}, nil
}

func (f *fakeIdentity) RequestPasswordReset(_ context.Context, req *plansv1.RequestPasswordResetRequest) (*plansv1.RequestPasswordResetResponse, error) {
	f.mu.Lock()
	f.resetReqs = append(f.resetReqs, req)
	f.mu.Unlock()
	if !strings.Contains(req.GetEmail(), "@") {
		return nil, errkey.New(codes.InvalidArgument, "common.invalid_argument", "bad address")
	}
	return &plansv1.RequestPasswordResetResponse{}, nil
}

func (f *fakeIdentity) ResetPassword(_ context.Context, req *plansv1.ResetPasswordRequest) (*plansv1.ResetPasswordResponse, error) {
	f.mu.Lock()
	f.resetPasswords = append(f.resetPasswords, req)
	f.mu.Unlock()
	if req.GetToken() != goodResetToken {
		return nil, errkey.New(codes.InvalidArgument, "identity.token.invalid", "bad link")
	}
	return &plansv1.ResetPasswordResponse{}, nil
}

func (f *fakeIdentity) SignOut(_ context.Context, req *plansv1.SignOutRequest) (*plansv1.SignOutResponse, error) {
	f.mu.Lock()
	f.revoked[req.GetToken()] = true
	f.mu.Unlock()
	return &plansv1.SignOutResponse{}, nil
}

func (f *fakeIdentity) GetSession(_ context.Context, req *plansv1.GetSessionRequest) (*plansv1.GetSessionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := tokenUsers[req.GetToken()]
	if !ok || f.revoked[req.GetToken()] {
		return nil, errkey.New(codes.Unauthenticated, "identity.session.invalid", "sign in again")
	}
	return &plansv1.GetSessionResponse{User: &plansv1.User{Id: u.id, Email: u.email, EmailVerified: u.verified}, Memberships: f.memberships(u.id), ExpireTime: timestamppb.New(time.Now().Add(time.Hour))}, nil
}

func (f *fakeIdentity) seen(user string) {
	f.mu.Lock()
	f.lastUser = user
	f.mu.Unlock()
}

func (f *fakeIdentity) GetMyAccount(_ context.Context, req *plansv1.GetMyAccountRequest) (*plansv1.GetMyAccountResponse, error) {
	f.seen(req.GetUserId())
	if r := f.role(req.GetUserId(), req.GetAccountId()); r != "" {
		return &plansv1.GetMyAccountResponse{Id: req.GetAccountId(), Name: "Account", Role: r}, nil
	}
	return nil, notFound("account")
}

func (f *fakeIdentity) ListMyApps(_ context.Context, req *plansv1.ListMyAppsRequest) (*plansv1.ListMyAppsResponse, error) {
	f.seen(req.GetUserId())
	if f.role(req.GetUserId(), req.GetAccountId()) == "" {
		return nil, notFound("account")
	}
	app := adaApp
	if req.GetAccountId() == bobAcct {
		app = bobApp
	}
	return &plansv1.ListMyAppsResponse{Apps: []*plansv1.App{appFor(app)}}, nil
}

func (f *fakeIdentity) CreateMyApp(_ context.Context, req *plansv1.CreateMyAppRequest) (*plansv1.CreateMyAppResponse, error) {
	f.seen(req.GetUserId())
	switch f.role(req.GetUserId(), req.GetAccountId()) {
	case "":
		return nil, notFound("account")
	case "viewer":
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "read-only")
	}
	return &plansv1.CreateMyAppResponse{App: &plansv1.App{Id: "new", AccountId: req.GetAccountId(), Name: req.GetName()}}, nil
}

func (f *fakeIdentity) mine(user, app string) (*plansv1.App, string) {
	a := appFor(app)
	if a == nil {
		return nil, ""
	}
	return a, f.role(user, a.GetAccountId())
}

func (f *fakeIdentity) GetMyApp(_ context.Context, req *plansv1.GetMyAppRequest) (*plansv1.GetMyAppResponse, error) {
	f.seen(req.GetUserId())
	a, role := f.mine(req.GetUserId(), req.GetAppId())
	if role == "" {
		return nil, notFound("app")
	}
	return &plansv1.GetMyAppResponse{App: a, Role: role}, nil
}

func (f *fakeIdentity) ResolveMyTenant(_ context.Context, req *plansv1.ResolveMyTenantRequest) (*plansv1.ResolveMyTenantResponse, error) {
	f.seen(req.GetUserId())
	for _, app := range []string{adaApp, bobApp} {
		for _, t := range appFor(app).GetTenants() {
			if t.GetId() == req.GetTenantId() {
				if role := f.role(req.GetUserId(), t.GetAccountId()); role != "" {
					return &plansv1.ResolveMyTenantResponse{Tenant: t, Role: role}, nil
				}
			}
		}
	}
	return nil, notFound("tenant")
}

func (f *fakeIdentity) CreateMyApiKey(_ context.Context, req *plansv1.CreateMyApiKeyRequest) (*plansv1.CreateMyApiKeyResponse, error) {
	f.seen(req.GetUserId())
	_, role := f.mine(req.GetUserId(), req.GetAppId())
	switch role {
	case "":
		return nil, notFound("app")
	case "viewer":
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "read-only")
	}
	return &plansv1.CreateMyApiKeyResponse{ApiKey: &plansv1.ApiKey{Id: "key-new", Prefix: "abcdefgh"}, Plaintext: "qk_live_abcdefgh_secret"}, nil
}

func (f *fakeIdentity) ListMyApiKeys(_ context.Context, req *plansv1.ListMyApiKeysRequest) (*plansv1.ListMyApiKeysResponse, error) {
	f.seen(req.GetUserId())
	if _, role := f.mine(req.GetUserId(), req.GetAppId()); role == "" {
		return nil, notFound("app")
	}
	return &plansv1.ListMyApiKeysResponse{ApiKeys: []*plansv1.ApiKey{{Id: "key-ada", Prefix: "abcdefgh"}}}, nil
}

func (f *fakeIdentity) RevokeMyApiKey(_ context.Context, req *plansv1.RevokeMyApiKeyRequest) (*plansv1.RevokeMyApiKeyResponse, error) {
	f.seen(req.GetUserId())
	_, role := f.mine(req.GetUserId(), req.GetAppId())
	if role == "" {
		return nil, notFound("app")
	}
	if f.keys[req.GetApiKeyId()] != req.GetAppId() {
		return nil, notFound("api_key")
	}
	return &plansv1.RevokeMyApiKeyResponse{ApiKey: &plansv1.ApiKey{Id: req.GetApiKeyId()}}, nil
}

// productPlans records the tenant each call carried.
type productPlans struct {
	plansv1.UnimplementedPlansServiceServer
	mu      sync.Mutex
	tenants []string
}

func (p *productPlans) saw(ctx context.Context) {
	t, _ := tenant.FromContext(ctx)
	p.mu.Lock()
	p.tenants = append(p.tenants, t.ID.String())
	p.mu.Unlock()
}

func (p *productPlans) ListCustomers(ctx context.Context, _ *plansv1.ListCustomersRequest) (*plansv1.ListCustomersResponse, error) {
	p.saw(ctx)
	return &plansv1.ListCustomersResponse{Customers: []*plansv1.Customer{{Id: "c1"}, {Id: "c2"}, {Id: "c3"}}}, nil
}

func (p *productPlans) CreateCustomer(ctx context.Context, req *plansv1.CreateCustomerRequest) (*plansv1.CreateCustomerResponse, error) {
	p.saw(ctx)
	return &plansv1.CreateCustomerResponse{Customer: &plansv1.Customer{Id: "c9", ExternalId: req.GetExternalId()}}, nil
}

func (p *productPlans) GetSubscriptionReport(ctx context.Context, _ *plansv1.GetSubscriptionReportRequest) (*plansv1.GetSubscriptionReportResponse, error) {
	p.saw(ctx)
	return &plansv1.GetSubscriptionReportResponse{Rows: []*plansv1.SubscriptionReportRow{{Currency: "USD", Active: 4, MrrMinor: 4900}, {Currency: "TRY", Active: 1, MrrMinor: 25000}}}, nil
}

type productWebhooks struct {
	plansv1.UnimplementedWebhookServiceServer
	failed int
}

func (p productWebhooks) ListWebhookEndpoints(context.Context, *plansv1.ListWebhookEndpointsRequest) (*plansv1.ListWebhookEndpointsResponse, error) {
	return &plansv1.ListWebhookEndpointsResponse{Endpoints: []*plansv1.WebhookEndpoint{{Id: "e1"}}}, nil
}

func (p productWebhooks) ListWebhookDeliveries(context.Context, *plansv1.ListWebhookDeliveriesRequest) (*plansv1.ListWebhookDeliveriesResponse, error) {
	out := &plansv1.ListWebhookDeliveriesResponse{}
	for i := 0; i < p.failed; i++ {
		out.Deliveries = append(out.Deliveries, &plansv1.WebhookDelivery{Id: "d"})
	}
	return out, nil
}

type productBilling struct {
	billingv1.UnimplementedBillingServiceServer
	mu      sync.Mutex
	tenants []string
}

func (b *productBilling) saw(ctx context.Context) {
	t, _ := tenant.FromContext(ctx)
	b.mu.Lock()
	b.tenants = append(b.tenants, t.ID.String())
	b.mu.Unlock()
}

func (b *productBilling) ListInvoices(ctx context.Context, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	b.saw(ctx)
	switch req.GetStatus() {
	case "open":
		return &billingv1.ListInvoicesResponse{Invoices: []*billingv1.Invoice{{Id: "i1"}, {Id: "i2"}}}, nil
	case "paid":
		return &billingv1.ListInvoicesResponse{Invoices: []*billingv1.Invoice{{Id: "i3"}}, NextPageToken: "more"}, nil
	}
	return &billingv1.ListInvoicesResponse{Invoices: []*billingv1.Invoice{{Id: "i3", Number: "INV-1", Status: "paid", Currency: "USD", TotalMinor: 4900}}}, nil
}

func (b *productBilling) GetRevenueReport(ctx context.Context, _ *billingv1.GetRevenueReportRequest) (*billingv1.GetRevenueReportResponse, error) {
	b.saw(ctx)
	return &billingv1.GetRevenueReportResponse{Rows: []*billingv1.RevenueRow{
		{Period: "2026-09", Currency: "USD", BilledMinor: 1000, CollectedMinor: 900, OutstandingMinor: 100},
		{Period: "2026-10", Currency: "USD", BilledMinor: 2000, CollectedMinor: 500, OutstandingMinor: 1500},
		{Period: "2026-10", Currency: "TRY", BilledMinor: 50000, CollectedMinor: 50000},
	}}, nil
}

type product struct {
	// ledgerPaths are the payments-worker paths the gateway asked, in order: they name the tenant whose ledger was read.
	ledgerMu    sync.Mutex
	ledgerPaths []string
	ts          *httptest.Server
	ident       *fakeIdentity
	plans       *productPlans
	billing     *productBilling
	limiter     *fakeLimiter
}

// newProduct starts the gateway with the fakes above and a fake payments-worker that answers ledger requests with `ledger` (nil: unreachable).
func newProduct(t *testing.T, ledger func() []map[string]any, tweak func(*Deps)) *product {
	t.Helper()
	p := &product{ident: newFakeIdentity(), plans: &productPlans{}, billing: &productBilling{}, limiter: &fakeLimiter{}}
	gs, _ := grpcx.NewServer(grpcx.ServerOptions{Extra: []grpc.ServerOption{grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor())}})
	plansv1.RegisterIdentityServiceServer(gs, p.ident)
	plansv1.RegisterPlansServiceServer(gs, p.plans)
	plansv1.RegisterWebhookServiceServer(gs, productWebhooks{failed: 2})
	plansv1.RegisterAdminServiceServer(gs, &fakePlans{})
	billingv1.RegisterBillingServiceServer(gs, p.billing)
	limitsv1.RegisterLimitsServiceServer(gs, productLimits{})
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
	deps := Deps{Identity: plansv1.NewIdentityServiceClient(cc), Plans: plansv1.NewPlansServiceClient(cc), Admin: plansv1.NewAdminServiceClient(cc),
		Webhooks: plansv1.NewWebhookServiceClient(cc), Billing: billingv1.NewBillingServiceClient(cc), Limits: limitsv1.NewLimitsServiceClient(cc), Auth: fakeAuth{}, Limiter: p.limiter,
		OperatorToken: operatorToken, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if ledger != nil {
		pw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/ledger"):
				p.ledgerMu.Lock()
				p.ledgerPaths = append(p.ledgerPaths, r.URL.Path)
				p.ledgerMu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"entries": ledger()})
			case strings.HasSuffix(r.URL.Path, "/provider-accounts"):
				_ = json.NewEncoder(w).Encode(map[string]any{"accounts": []map[string]any{{"provider": "iyzico", "environment": "live", "is_default": true}}})
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(pw.Close)
		u, _ := url.Parse(pw.URL)
		deps.PaymentsURL = u
	}
	if tweak != nil {
		tweak(&deps)
	}
	p.ts = httptest.NewServer(New(deps))
	t.Cleanup(p.ts.Close)
	return p
}

func (p *product) call(t *testing.T, method, path, token, tenantID, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, p.ts.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if tenantID != "" {
		req.Header.Set("X-Quotify-Tenant", tenantID)
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

func errKeyOf(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	k, _ := e["key"].(string)
	return k
}

func TestSignInAndSignUp(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "POST", "/auth/signin", "", "", `{"email":"ada@example.com","password":"right password!"}`)
	if resp.StatusCode != 200 || m["token"] != tokAda || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("sign-in: %d %v cache=%q", resp.StatusCode, m, resp.Header.Get("Cache-Control"))
	}
	resp, m = p.call(t, "POST", "/auth/signin", "", "", `{"email":"ada@example.com","password":"nope"}`)
	if resp.StatusCode != 401 || errKeyOf(m) != "identity.auth.invalid_credentials" {
		t.Fatalf("wrong password: %d %v", resp.StatusCode, m)
	}
	resp, m = p.call(t, "POST", "/auth/signup", "", "", `{"email":"new@example.com","password":"a long enough password","account_name":"New"}`)
	if resp.StatusCode != 202 || m["token"] != nil || m["status"] != "accepted" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("sign-up must answer 202 and open no session: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", "/auth/signin", "", "", `{not json`); resp.StatusCode != 400 {
		t.Errorf("malformed body: %d", resp.StatusCode)
	}
}

func TestSignInIsRateLimitedPerAddress(t *testing.T) {
	p := newProduct(t, nil, nil)
	var last int
	for i := 0; i < signInsPerMinute+2; i++ {
		resp, m := p.call(t, "POST", "/auth/signin", "", "", `{"email":"ada@example.com","password":"nope"}`)
		last = resp.StatusCode
		if i >= signInsPerMinute && (resp.StatusCode != 429 || errKeyOf(m) != "identity.auth.rate_limited" || resp.Header.Get("Retry-After") == "") {
			t.Fatalf("attempt %d: %d %v", i+1, resp.StatusCode, m)
		}
	}
	if last != 429 {
		t.Fatalf("last = %d", last)
	}
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if p.ident.signIns != signInsPerMinute {
		t.Errorf("plans-svc saw %d attempts, want %d: throttled ones must not reach it", p.ident.signIns, signInsPerMinute)
	}
}

func TestClientAddrTrustsForwardedForOnlyFromPrivateProxies(t *testing.T) {
	s := &Server{d: Deps{TrustForwardedFor: true}}
	mk := func(remote, xff string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	for _, c := range []struct{ remote, xff, want string }{
		{"10.0.0.5:1234", "203.0.113.9", "203.0.113.9"},
		{"10.0.0.5:1234", "198.51.100.1, 203.0.113.9", "203.0.113.9"},
		{"203.0.113.50:1234", "198.51.100.1", "203.0.113.50"}, // a public peer cannot choose its bucket
		{"10.0.0.5:1234", "not-an-ip", "10.0.0.5"},
		{"10.0.0.5:1234", "", "10.0.0.5"},
	} {
		if got := s.clientAddr(mk(c.remote, c.xff)); got != c.want {
			t.Errorf("remote %s xff %q: %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
	if got := (&Server{}).clientAddr(mk("10.0.0.5:1", "203.0.113.9")); got != "10.0.0.5" {
		t.Errorf("without the setting the header is ignored: %s", got)
	}
}

func TestClientAddrTrustedProxySecretAndNetworks(t *testing.T) {
	const secret = "edge-secret-0123456789-0123456789-xx"
	s := &Server{d: Deps{TrustedProxySecret: secret, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}}}
	mk := func(remote, edge string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		if edge != "" {
			r.Header.Set(EdgeAuthHeader, edge)
		}
		return r
	}
	for _, c := range []struct{ name, remote, edge, want string }{
		{"secret ok from a public peer", "192.0.2.7:1", secret, "203.0.113.9"},
		{"wrong secret", "192.0.2.7:1", secret + "x", "192.0.2.7"},
		{"shorter secret", "192.0.2.7:1", "edge", "192.0.2.7"},
		{"no secret: a direct client spoofing the header", "192.0.2.7:1", "", "192.0.2.7"},
		{"peer inside a trusted network", "198.51.100.20:1", "", "203.0.113.9"},
		{"peer just outside the network", "198.51.101.20:1", "", "198.51.101.20"},
		{"a private peer is not trusted without TrustForwardedFor", "10.0.0.5:1", "", "10.0.0.5"},
	} {
		if got := s.clientAddr(mk(c.remote, c.edge)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// Without a configured secret the header proves nothing.
	if got := (&Server{}).clientAddr(mk("192.0.2.7:1", "anything")); got != "192.0.2.7" {
		t.Errorf("no secret configured: %s", got)
	}
}

func TestSessionRoutesNeedALiveSession(t *testing.T) {
	p := newProduct(t, nil, nil)
	for _, c := range []struct {
		name, token string
	}{
		{"no token", ""},
		{"unknown session", "qs_unknown"},
		{"an API key", "live-key"},
		{"the operator token", operatorToken},
		{"expired", "qs_expired"},
	} {
		for _, path := range []string{"/auth/me", "/app/v1/account", "/app/v1/apps"} {
			resp, m := p.call(t, "GET", path, c.token, "", "")
			if resp.StatusCode != 401 {
				t.Errorf("%s on %s: %d %v", c.name, path, resp.StatusCode, m)
			}
		}
	}
	resp, m := p.call(t, "GET", "/auth/me", tokAda, "", "")
	if resp.StatusCode != 200 || m["user"].(map[string]any)["email"] != "ada@example.com" {
		t.Fatalf("me: %d %v", resp.StatusCode, m)
	}
	// Sign out revokes the session at once.
	if resp, _ := p.call(t, "POST", "/auth/signout", tokAda, "", ""); resp.StatusCode != 204 {
		t.Fatalf("sign-out: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "GET", "/auth/me", tokAda, "", ""); resp.StatusCode != 401 {
		t.Errorf("a signed-out session still works: %d", resp.StatusCode)
	}
}

func TestSessionsCannotCallOperatorRoutes(t *testing.T) {
	p := newProduct(t, nil, nil)
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/v1/tenants"}, {"POST", "/admin/v1/accounts"}, {"GET", "/admin/v1/accounts/" + adaAcct + "/apps"},
		{"GET", "/admin/v1/tenants/" + adaLive + "/ledger"}, {"POST", "/admin/v1/accounts/" + adaAcct + "/apps"},
	} {
		// With and without naming a tenant: a session is never an operator.
		for _, tn := range []string{"", adaLive} {
			if resp, m := p.call(t, c.method, c.path, tokAda, tn, `{}`); resp.StatusCode != 403 || errKeyOf(m) != "identity.operator.forbidden" {
				t.Errorf("%s %s with a session (tenant %q): %d", c.method, c.path, tn, resp.StatusCode)
			}
		}
	}
}

func TestPeopleSeeOnlyTheirOwnAccounts(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return nil }, nil)
	// Ada reads hers.
	if resp, m := p.call(t, "GET", "/app/v1/apps", tokAda, "", ""); resp.StatusCode != 200 || len(m["apps"].([]any)) != 1 {
		t.Fatalf("Ada's apps: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp, tokAda, "", ""); resp.StatusCode != 200 || m["role"] != "owner" {
		t.Fatalf("Ada's app: %d %v", resp.StatusCode, m)
	}
	// Bob asks for Ada's things, every way there is.
	type ask struct{ method, path, body string }
	for _, a := range []ask{
		{"GET", "/app/v1/apps/" + adaApp, ""},
		{"GET", "/app/v1/apps/" + adaApp + "/overview", ""},
		{"GET", "/app/v1/apps/" + adaApp + "/overview?environment=sandbox", ""},
		{"GET", "/app/v1/apps/" + adaApp + "/api-keys", ""},
		{"POST", "/app/v1/apps/" + adaApp + "/api-keys", `{"environment":"live"}`},
		{"DELETE", "/app/v1/apps/" + adaApp + "/api-keys/key-ada", ""},
		{"GET", "/app/v1/account?account_id=" + adaAcct, ""},
		{"GET", "/app/v1/apps?account_id=" + adaAcct, ""},
		{"POST", "/app/v1/apps?account_id=" + adaAcct, `{"name":"Sneaky","currency":"USD","timezone":"UTC"}`},
	} {
		resp, m := p.call(t, a.method, a.path, tokBob, "", a.body)
		if resp.StatusCode != 404 {
			t.Errorf("Bob %s %s: %d %v, want 404", a.method, a.path, resp.StatusCode, m)
		}
	}
	// Bob's key list of his own app does not reveal Ada's key through a mixed path.
	if resp, _ := p.call(t, "DELETE", "/app/v1/apps/"+bobApp+"/api-keys/key-ada", tokBob, "", ""); resp.StatusCode != 404 {
		t.Errorf("revoking Ada's key through Bob's app: %d", resp.StatusCode)
	}
	// Nothing Bob did reached the overview upstreams for Ada's tenants.
	for _, tn := range append(append([]string{}, p.plans.tenants...), p.billing.tenants...) {
		if tn == adaLive || tn == adaSandbox {
			t.Errorf("an upstream call carried Ada's tenant %s on behalf of Bob", tn)
		}
	}
}

func TestBodiesCannotNameTheUser(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "POST", "/app/v1/apps", tokBob, "", `{"name":"Mine","currency":"USD","timezone":"UTC","user_id":"`+adaUser+`","account_id":"`+adaAcct+`"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %v", resp.StatusCode, m)
	}
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if p.ident.lastUser != bobUser {
		t.Errorf("the call carried user %s, want the session's user (body fields are ignored)", p.ident.lastUser)
	}
}

func TestViewerReadsButChangesNothing(t *testing.T) {
	p := newProduct(t, nil, nil)
	if resp, _ := p.call(t, "GET", "/app/v1/apps/"+adaApp, tokViewer, "", ""); resp.StatusCode != 200 {
		t.Errorf("viewer reads: %d", resp.StatusCode)
	}
	resp, m := p.call(t, "POST", "/app/v1/apps", tokViewer, "", `{"name":"X","currency":"USD","timezone":"UTC"}`)
	if resp.StatusCode != 403 || errKeyOf(m) != "identity.account.forbidden" {
		t.Errorf("viewer creates an app: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys", tokViewer, "", `{"environment":"live"}`); resp.StatusCode != 403 {
		t.Errorf("viewer creates a key: %d", resp.StatusCode)
	}
}

func TestKeysAreShownOnce(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys", tokAda, "", `{"environment":"live"}`)
	if resp.StatusCode != 201 || m["plaintext"] != "qk_live_abcdefgh_secret" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("create key: %d %v cache=%q", resp.StatusCode, m, resp.Header.Get("Cache-Control"))
	}
	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys", tokAda, "", `{"environment":"staging"}`); resp.StatusCode != 400 {
		t.Errorf("bad environment: %d", resp.StatusCode)
	}
	raw, _ := json.Marshal(func() map[string]any {
		_, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/api-keys", tokAda, "", "")
		return m
	}())
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "plaintext") {
		t.Errorf("the key list carries a secret: %s", raw)
	}
	if resp, _ := p.call(t, "DELETE", "/app/v1/apps/"+adaApp+"/api-keys/key-ada", tokAda, "", ""); resp.StatusCode != 204 {
		t.Errorf("revoke: %d", resp.StatusCode)
	}
}

func TestSessionActsOnItsOwnTenantsOnly(t *testing.T) {
	p := newProduct(t, nil, nil)
	body := `{"external_id":"cus_1","name":"Ada","email":"a@x.io"}`
	// Ada acts on her live tenant: the upstream sees that tenant.
	if resp, m := p.call(t, "POST", "/v1/customers", tokAda, adaLive, body); resp.StatusCode != 201 {
		t.Fatalf("own tenant: %d %v", resp.StatusCode, m)
	}
	if got := p.plans.tenants[len(p.plans.tenants)-1]; got != adaLive {
		t.Errorf("plans-svc saw tenant %s", got)
	}
	before := len(p.plans.tenants)
	// Ada names Bob's tenants, or nothing, or rubbish: refused before any upstream is called.
	for _, c := range []struct {
		tn   string
		want int
	}{{bobLive, 404}, {bobSandbox, 404}, {"", 401}, {"not-a-uuid", 404}, {"00000000-0000-4000-8000-000000000000", 404}} {
		if resp, m := p.call(t, "POST", "/v1/customers", tokAda, c.tn, body); resp.StatusCode != c.want {
			t.Errorf("Ada acting on %q: %d %v, want %d", c.tn, resp.StatusCode, m, c.want)
		}
	}
	if len(p.plans.tenants) != before {
		t.Errorf("a refused call reached plans-svc")
	}
	// Bob cannot act on Ada's, a signed-out or unknown session cannot act at all.
	if resp, _ := p.call(t, "POST", "/v1/customers", tokBob, adaLive, body); resp.StatusCode != 404 {
		t.Errorf("Bob on Ada's tenant: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "POST", "/v1/customers", "qs_expired", adaLive, body); resp.StatusCode != 401 {
		t.Errorf("expired session acting: %d", resp.StatusCode)
	}
	// A viewer may read the tenant but not write to it.
	if resp, _ := p.call(t, "GET", "/v1/customers", tokViewer, adaLive, ""); resp.StatusCode == 403 || resp.StatusCode == 401 || resp.StatusCode == 404 {
		t.Errorf("viewer reading: %d", resp.StatusCode)
	}
	if resp, m := p.call(t, "POST", "/v1/customers", tokViewer, adaLive, body); resp.StatusCode != 403 || errKeyOf(m) != "identity.account.forbidden" {
		t.Errorf("viewer writing: %d %v", resp.StatusCode, m)
	}
	// Sandbox-only routes still follow the tenant's environment.
	if resp, _ := p.call(t, "POST", "/v1/sandbox/close-period", tokAda, adaLive, `{}`); resp.StatusCode != 403 {
		t.Errorf("a sandbox route on a live tenant: %d", resp.StatusCode)
	}
}

func ledgerRows(now time.Time) []map[string]any {
	row := func(id int, kind, status, cur string, amount int, ref string, ago time.Duration) map[string]any {
		return map[string]any{"id": id, "invoice_id": "inv", "provider": "iyzico", "environment": "live", "kind": kind, "status": status,
			"amount_minor": amount, "currency": cur, "provider_ref": ref, "occurred_at": now.Add(-ago).Format(time.RFC3339)}
	}
	return []map[string]any{
		row(9, "charge", "succeeded", "USD", 4900, "p1", time.Hour),
		row(8, "charge", "pending", "USD", 4900, "p1", 2*time.Hour), // the same payment before it settled
		row(7, "charge", "declined", "USD", 1500, "p2", 3*time.Hour),
		row(6, "charge", "succeeded", "TRY", 25000, "p3", 26*time.Hour),
		row(5, "charge", "pending", "USD", 700, "p4", time.Minute),
		row(4, "refund", "succeeded", "USD", 900, "r1", 5*time.Hour),
		row(3, "charge", "succeeded", "USD", 100, "old", 40*24*time.Hour), // outside the window
		row(2, "payout", "succeeded", "USD", 100000, "po", time.Hour),     // not a payment
	}
}

func TestOverviewComposesTheTenantsOwnNumbers(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return ledgerRows(time.Now()) }, nil)
	resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/overview?environment=live", tokAda, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("overview: %d %v", resp.StatusCode, m)
	}
	if m["tenant_id"] != adaLive || m["environment"] != "live" || m["currency"] != "USD" || m["status"] != "attention" {
		t.Errorf("header = %v", m)
	}
	if resp.Header.Get("Cache-Control") == "" {
		t.Error("an overview must say how it may be cached")
	}
	pay := m["payments"].(map[string]any)
	counts := map[string]float64{}
	for _, s := range pay["by_status"].([]any) {
		counts[s.(map[string]any)["status"].(string)] = s.(map[string]any)["count"].(float64)
	}
	if counts["paid"] != 2 || counts["failed"] != 1 || counts["processing"] != 1 || counts["refunded"] != 1 {
		t.Errorf("by_status = %v (a pending row followed by a success is one paid payment; old rows and payouts are not counted)", counts)
	}
	// Money stays under its currency.
	usd, try := map[string]any{}, map[string]any{}
	for _, c := range pay["by_currency"].([]any) {
		switch c.(map[string]any)["currency"] {
		case "USD":
			usd = c.(map[string]any)
		case "TRY":
			try = c.(map[string]any)
		}
	}
	if usd["paid_minor"] != float64(4900) || usd["failed_minor"] != float64(1500) || usd["refunded_minor"] != float64(900) || try["paid_minor"] != float64(25000) {
		t.Errorf("by_currency usd=%v try=%v", usd, try)
	}
	if len(pay["series"].([]any)) != 2 || len(pay["series"].([]any)[0].(map[string]any)["points"].([]any)) != overviewWindowDays {
		t.Errorf("series = %v", pay["series"])
	}
	inv := m["invoices"].(map[string]any)
	if inv["open"].(map[string]any)["count"] != float64(2) || inv["paid"].(map[string]any)["more"] != true {
		t.Errorf("invoices = %v", inv)
	}
	rev := inv["revenue"].([]any)
	if len(rev) != 2 || rev[1].(map[string]any)["currency"] != "USD" || rev[1].(map[string]any)["billed_minor"] != float64(3000) || rev[1].(map[string]any)["outstanding_minor"] != float64(1600) {
		t.Errorf("revenue sums periods of one currency and never currencies: %v", rev)
	}
	if m["customers"].(map[string]any)["count"] != float64(3) || len(m["subscriptions"].([]any)) != 2 {
		t.Errorf("customers/subscriptions = %v / %v", m["customers"], m["subscriptions"])
	}
	h := m["health"].(map[string]any)
	if h["webhook_failed"] != float64(2) || h["payments_reachable"] != true || len(h["providers"].([]any)) != 1 {
		t.Errorf("health = %v", h)
	}
	// Every upstream call carried the environment's tenant, nothing else.
	for _, tn := range append(append([]string{}, p.plans.tenants...), p.billing.tenants...) {
		if tn != adaLive {
			t.Errorf("an upstream call carried tenant %s", tn)
		}
	}
	// The sandbox is the other tenant.
	if _, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/overview?environment=sandbox", tokAda, "", ""); m["tenant_id"] != adaSandbox {
		t.Errorf("sandbox overview = %v", m["tenant_id"])
	}
	if resp, _ := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/overview?environment=prod", tokAda, "", ""); resp.StatusCode != 400 {
		t.Errorf("bad environment: %d", resp.StatusCode)
	}
}

func TestOverviewDegradesWhenPaymentsAreDown(t *testing.T) {
	p := newProduct(t, nil, nil) // no payments-worker
	resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/overview", tokAda, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("overview: %d %v", resp.StatusCode, m)
	}
	if m["status"] != "degraded" || m["payments"] != nil || m["invoices"] == nil {
		t.Errorf("status %v payments %v invoices %v: only the payments section is lost", m["status"], m["payments"], m["invoices"] != nil)
	}
	un := m["unavailable"].([]any)
	if len(un) != 1 || un[0] != "payments" || m["health"].(map[string]any)["payments_reachable"] != false {
		t.Errorf("unavailable = %v", un)
	}
}

func TestAggregatePaymentsWindowAndTruncation(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	mk := func(id int, ago time.Duration) ledgerEntry {
		return ledgerEntry{ID: json.Number(string(rune('0' + id%10))), Kind: "charge", Status: "succeeded", AmountMinor: "100", Currency: "USD", ProviderRef: "r" + string(rune('a'+id%26)) + string(rune('a'+id/26)), OccurredAt: now.Add(-ago)}
	}
	rows := make([]ledgerEntry, 0, ledgerWindowRows)
	for i := 0; i < ledgerWindowRows; i++ {
		rows = append(rows, mk(i, time.Duration(i)*time.Minute)) // all inside 30 days: the window may hold more
	}
	v := aggregatePayments(rows, now, 30, time.UTC, 3)
	if !v.Truncated || len(v.Recent) != 3 {
		t.Errorf("truncated=%v recent=%d", v.Truncated, len(v.Recent))
	}
	old := append([]ledgerEntry{}, rows...)
	old[len(old)-1].OccurredAt = now.Add(-45 * 24 * time.Hour)
	if aggregatePayments(old, now, 30, time.UTC, 3).Truncated {
		t.Error("when the oldest row read is outside the window nothing is missing")
	}
	// A day boundary follows the time zone.
	loc, _ := time.LoadLocation("Asia/Tokyo")
	late := []ledgerEntry{{ID: "1", Kind: "charge", Status: "succeeded", AmountMinor: "500", Currency: "JPY", ProviderRef: "x", OccurredAt: time.Date(2026, 10, 20, 16, 0, 0, 0, time.UTC)}}
	s := aggregatePayments(late, now, 30, loc, 1).Series[0].Points
	if s[len(s)-1].Date != "2026-10-21" || s[len(s)-1].AmountMinor != 500 {
		t.Errorf("series tail = %v", s[len(s)-1])
	}
}

func TestPublicAuthErrorsAreKeyed(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "GET", "/app/v1/apps", "", "", "")
	if resp.StatusCode != 401 || errKeyOf(m) != "identity.session.invalid" {
		t.Errorf("no token: %d %v", resp.StatusCode, m)
	}
	if got := status.Code(notFound("app")); got != codes.NotFound {
		t.Error("sanity")
	}
}

func TestSignUpAnswersTheSameForEveryAddress(t *testing.T) {
	p := newProduct(t, nil, nil)
	body := `{"email":"%s","password":"a long enough password","account_name":"New","locale":"%s"}`
	_, a := p.call(t, "POST", "/auth/signup", "", "", strings.Replace(strings.Replace(body, "%s", "free@example.com", 1), "%s", "tr", 1))
	_, b := p.call(t, "POST", "/auth/signup", "", "", strings.Replace(strings.Replace(body, "%s", "ada@example.com", 1), "%s", "xx", 1))
	if len(a) != len(b) || a["status"] != b["status"] {
		t.Fatalf("answers differ: %v vs %v", a, b)
	}
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if len(p.ident.signUpReqs) != 2 || p.ident.signUpReqs[0].GetLocale() != "tr" || p.ident.signUpReqs[1].GetLocale() != "en" {
		t.Errorf("locale: %v", p.ident.signUpReqs)
	}
}

func TestLocaleOf(t *testing.T) {
	mk := func(h string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		if h != "" {
			r.Header.Set("Accept-Language", h)
		}
		return r
	}
	for _, c := range []struct{ asked, header, want string }{
		{"de", "", "de"}, {"DE", "fr", "de"}, {"", "tr-TR,tr;q=0.9,en;q=0.8", "tr"}, {"", "xx, es-MX;q=0.5", "es"}, {"", "ar", "ar"},
		{"klingon", "", "en"}, {"", "", "en"}, {"", "ja,zh", "en"},
	} {
		if got := localeOf(mk(c.header), c.asked); got != c.want {
			t.Errorf("asked %q header %q: %s, want %s", c.asked, c.header, got, c.want)
		}
	}
}

func TestVerifyEmailRoute(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "POST", "/auth/verify", "", "", `{"token":"`+goodVerifyToken+`"}`)
	u, _ := m["user"].(map[string]any)
	if resp.StatusCode != 200 || u["email_verified"] != true || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("verify: %d %v", resp.StatusCode, m)
	}
	resp, m = p.call(t, "POST", "/auth/verify", "", "", `{"token":"qv_used"}`)
	if resp.StatusCode != 400 || errKeyOf(m) != "identity.token.invalid" {
		t.Fatalf("bad token: %d %v", resp.StatusCode, m)
	}
}

func TestResendVerificationNeedsASessionAndIsThrottled(t *testing.T) {
	p := newProduct(t, nil, nil)
	for _, tok := range []string{"", "qs_unknown", operatorToken, "live-key"} {
		if resp, _ := p.call(t, "POST", "/auth/resend-verification", tok, "", ""); resp.StatusCode != 401 {
			t.Errorf("token %q: %d, want 401", tok, resp.StatusCode)
		}
	}
	if resp, _ := p.call(t, "POST", "/auth/resend-verification", tokAda, "", ""); resp.StatusCode != 204 {
		t.Fatalf("resend: %d", resp.StatusCode)
	}
	p.ident.mu.Lock()
	user := p.ident.lastUser
	p.ident.mu.Unlock()
	if user != adaUser {
		t.Errorf("the user comes from the session: %s", user)
	}
	resp, m := p.call(t, "POST", "/auth/resend-verification", tokAda, "", "")
	if resp.StatusCode != 429 || errKeyOf(m) != "identity.verify.throttled" {
		t.Fatalf("throttled resend: %d %v", resp.StatusCode, m)
	}
}

func TestForgotPasswordNeverSaysWhetherTheAddressExists(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp1, a := p.call(t, "POST", "/auth/forgot", "", "", `{"email":"ada@example.com"}`)
	resp2, b := p.call(t, "POST", "/auth/forgot", "", "", `{"email":"nobody@example.com","locale":"fr"}`)
	if resp1.StatusCode != 202 || resp2.StatusCode != 202 || a["status"] != b["status"] || resp1.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("forgot: %d %v / %d %v", resp1.StatusCode, a, resp2.StatusCode, b)
	}
	if resp, m := p.call(t, "POST", "/auth/forgot", "", "", `{"email":"not an address"}`); resp.StatusCode != 400 {
		t.Errorf("malformed address: %d %v", resp.StatusCode, m)
	}
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if len(p.ident.resetReqs) != 3 || p.ident.resetReqs[1].GetLocale() != "fr" || p.ident.resetReqs[0].GetLocale() != "en" {
		t.Errorf("requests: %v", p.ident.resetReqs)
	}
}

func TestForgotPasswordIsRateLimitedPerAddress(t *testing.T) {
	p := newProduct(t, nil, nil)
	for i := 0; i < forgotsPerMinute+2; i++ {
		resp, m := p.call(t, "POST", "/auth/forgot", "", "", `{"email":"ada@example.com"}`)
		if i >= forgotsPerMinute && (resp.StatusCode != 429 || errKeyOf(m) != "identity.auth.rate_limited") {
			t.Fatalf("request %d: %d %v", i+1, resp.StatusCode, m)
		}
	}
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if len(p.ident.resetReqs) != forgotsPerMinute {
		t.Errorf("plans-svc saw %d requests, want %d", len(p.ident.resetReqs), forgotsPerMinute)
	}
}

func TestResetPasswordRoute(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, _ := p.call(t, "POST", "/auth/reset", "", "", `{"token":"`+goodResetToken+`","password":"a brand new password"}`)
	if resp.StatusCode != 204 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("reset: %d", resp.StatusCode)
	}
	resp, m := p.call(t, "POST", "/auth/reset", "", "", `{"token":"qr_bad","password":"a brand new password"}`)
	if resp.StatusCode != 400 || errKeyOf(m) != "identity.token.invalid" {
		t.Fatalf("bad token: %d %v", resp.StatusCode, m)
	}
	// A body cannot name the user: only token and password are read.
	p.call(t, "POST", "/auth/reset", "", "", `{"token":"`+goodResetToken+`","password":"a brand new password","user_id":"`+bobUser+`"}`)
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if last := p.ident.resetPasswords[len(p.ident.resetPasswords)-1]; last.GetNewPassword() != "a brand new password" {
		t.Errorf("last = %v", last)
	}
}

func TestSpendingATokenIsRateLimitedPerAddress(t *testing.T) {
	p := newProduct(t, nil, nil)
	for i := 0; i < tokenUsesPerMinute+2; i++ {
		resp, m := p.call(t, "POST", "/auth/verify", "", "", `{"token":"qv_guess"}`)
		if i >= tokenUsesPerMinute && (resp.StatusCode != 429 || errKeyOf(m) != "identity.auth.rate_limited") {
			t.Fatalf("request %d: %d %v", i+1, resp.StatusCode, m)
		}
	}
}
