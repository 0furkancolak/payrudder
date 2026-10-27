package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Payrudder as a SaaS (phase 55) at the gateway, against fakes of plans-svc, limits-svc, ingest-svc and billing-svc: the rules of the plans are tested in plans-svc
// against Postgres; what matters here is the edge: what is refused at ingest, what is metered and where, who may make a payment link, and that nothing exists
// with the mode off.

const (
	platformTenantID = "0193c000-0000-7000-8000-0000000000f1"
	saasCustomer     = "0193c000-0000-7000-8000-0000000000c1"
)

type fakeSaas struct {
	plansv1.UnimplementedSaasServiceServer
	mu          sync.Mutex
	policyCalls int
	policyErr   error
	metered     map[string]bool // tenant id to whether its events are metered
}

func platformRef() *plansv1.PlatformTenant {
	return &plansv1.PlatformTenant{Id: platformTenantID, Environment: "live", Timezone: "UTC", Currency: "USD"}
}

func (f *fakeSaas) GetPlatform(context.Context, *plansv1.GetPlatformRequest) (*plansv1.GetPlatformResponse, error) {
	return &plansv1.GetPlatformResponse{Enabled: true, Tenant: platformRef()}, nil
}

func (f *fakeSaas) GetMyPlan(_ context.Context, req *plansv1.GetMyPlanRequest) (*plansv1.GetMyPlanResponse, error) {
	plan := &plansv1.OrganizationPlan{Enabled: true, AccountId: req.GetAccountId(), State: "active", CanManage: req.GetUserId() != viewerUser, Platform: platformRef(), CustomerId: saasCustomer}
	plan.Plan = &plansv1.PlanOffer{Code: "free", Name: "Free", Version: 1, Currency: "USD", Interval: "month", Current: true}
	plan.Quotas = append(plan.Quotas, &plansv1.PlanQuota{Resource: "events", Limit: 10000, Mode: "hard"})
	plan.Quotas = append(plan.Quotas, &plansv1.PlanQuota{Resource: "apps", Used: 1, Limit: 1, Mode: "hard", OverLimit: true})
	return &plansv1.GetMyPlanResponse{Plan: plan}, nil
}

func (f *fakeSaas) ChangeMyPlan(ctx context.Context, req *plansv1.ChangeMyPlanRequest) (*plansv1.ChangeMyPlanResponse, error) {
	if req.GetPlanCode() == "free" {
		return nil, errkey.New(codes.FailedPrecondition, "plans.saas.same_plan", "the account is on that plan already")
	}
	r, err := f.GetMyPlan(ctx, &plansv1.GetMyPlanRequest{UserId: req.GetUserId(), AccountId: req.GetAccountId()})
	if err != nil {
		return nil, err
	}
	r.Plan.Plan = &plansv1.PlanOffer{Code: req.GetPlanCode(), Current: true}
	return &plansv1.ChangeMyPlanResponse{Plan: r.Plan}, nil
}

func (f *fakeSaas) ResolveIngestPolicy(_ context.Context, req *plansv1.ResolveIngestPolicyRequest) (*plansv1.ResolveIngestPolicyResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policyCalls++
	if f.policyErr != nil {
		return nil, f.policyErr
	}
	if !f.metered[req.GetTenantId()] {
		return &plansv1.ResolveIngestPolicyResponse{}, nil
	}
	return &plansv1.ResolveIngestPolicyResponse{Metered: true, AccountId: adaAcct, Platform: platformRef(), CustomerId: saasCustomer, CustomerExternalId: adaAcct, MeterCode: "events"}, nil
}

type saasLimits struct {
	limitsv1.UnimplementedLimitsServiceServer
	mu      sync.Mutex
	allowed bool
	err     error
	asked   int
}

func (f *saasLimits) CheckEntitlement(ctx context.Context, req *limitsv1.CheckEntitlementRequest) (*limitsv1.CheckEntitlementResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	if ti, _ := tenant.FromContext(ctx); ti.ID.String() != platformTenantID || req.GetCustomerId() != saasCustomer {
		return nil, status.Error(codes.Internal, "the quota is read as the platform tenant, for the organisation's customer row")
	}
	if f.err != nil {
		return nil, f.err
	}
	st := &limitsv1.LimitStatus{CustomerId: saasCustomer, MeterCode: req.GetMeterCode(), UsedUnits: 123, LimitUnits: 10000, Allowed: f.allowed, ResetsAt: timestamppb.New(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))}
	return &limitsv1.CheckEntitlementResponse{Status: st}, nil
}

type saasIngest struct {
	ingestv1.UnimplementedIngestServiceServer
	mu    sync.Mutex
	calls []saasIngestCall
}

type saasIngestCall struct {
	tenant tenant.Info
	req    *ingestv1.IngestEventsRequest
}

func (f *saasIngest) IngestEvents(ctx context.Context, req *ingestv1.IngestEventsRequest) (*ingestv1.IngestEventsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ti, _ := tenant.FromContext(ctx)
	f.calls = append(f.calls, saasIngestCall{tenant: ti, req: req})
	resp := &ingestv1.IngestEventsResponse{}
	for i, e := range req.GetEvents() {
		st := ingestv1.EventResult_STATUS_ACCEPTED
		if e.GetMeterCode() == "dup" {
			st = ingestv1.EventResult_STATUS_DUPLICATE
		}
		resp.Results = append(resp.Results, &ingestv1.EventResult{Status: st, EventId: "ev" + string(rune('0'+i))})
	}
	return resp, nil
}

type saasBilling struct {
	billingv1.UnimplementedBillingServiceServer
	links []*billingv1.CreatePaymentLinkRequest
}

func (f *saasBilling) ListInvoices(_ context.Context, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	if req.GetCustomerId() != saasCustomer {
		return nil, status.Error(codes.Internal, "invoices are asked for the organisation's customer row")
	}
	return &billingv1.ListInvoicesResponse{Invoices: []*billingv1.Invoice{
		{Id: "i-open", CustomerId: saasCustomer, Number: "INV-1", Status: "open", Currency: "USD", TotalMinor: 2900},
		{Id: "i-draft", CustomerId: saasCustomer, Status: "draft", Currency: "USD", TotalMinor: 2900},
		{Id: "i-zero", CustomerId: saasCustomer, Number: "INV-0", Status: "paid", Currency: "USD", TotalMinor: 0},
	}}, nil
}

func (f *saasBilling) GetInvoice(_ context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.GetInvoiceResponse, error) {
	switch req.GetInvoiceId() {
	case "0193c000-0000-7000-8000-0000000000a1":
		return &billingv1.GetInvoiceResponse{Invoice: &billingv1.Invoice{Id: req.GetInvoiceId(), CustomerId: saasCustomer, Status: "open", TotalMinor: 2900}}, nil
	case "0193c000-0000-7000-8000-0000000000a2":
		return &billingv1.GetInvoiceResponse{Invoice: &billingv1.Invoice{Id: req.GetInvoiceId(), CustomerId: "someone-else", Status: "open", TotalMinor: 2900}}, nil
	case "0193c000-0000-7000-8000-0000000000a3":
		return &billingv1.GetInvoiceResponse{Invoice: &billingv1.Invoice{Id: req.GetInvoiceId(), CustomerId: saasCustomer, Status: "paid", TotalMinor: 2900}}, nil
	}
	return nil, status.Error(codes.NotFound, "no such invoice")
}

func (f *saasBilling) CreatePaymentLink(_ context.Context, req *billingv1.CreatePaymentLinkRequest) (*billingv1.CreatePaymentLinkResponse, error) {
	f.links = append(f.links, req)
	return &billingv1.CreatePaymentLinkResponse{Token: "tok123"}, nil
}

func (f *saasBilling) CreatePortalLink(_ context.Context, req *billingv1.CreatePortalLinkRequest) (*billingv1.CreatePortalLinkResponse, error) {
	return &billingv1.CreatePortalLinkResponse{Token: "ptok", ExpireTime: timestamppb.New(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))}, nil
}

type saasAdmin struct {
	plansv1.UnimplementedAdminServiceServer
}

func (saasAdmin) ListTenants(context.Context, *plansv1.ListTenantsRequest) (*plansv1.ListTenantsResponse, error) {
	return &plansv1.ListTenantsResponse{Tenants: []*plansv1.Tenant{{Id: adaLive}, {Id: platformTenantID}, {Id: adaSandbox}}}, nil
}

// saasStack is the gateway with the SaaS fakes behind it. on false leaves Deps.Saas nil, as PAYRUDDER_SAAS=off does.
type saasStack struct {
	*product
	saas    *fakeSaas
	limits  *saasLimits
	ingest  *saasIngest
	billing *saasBilling
}

func newSaasStack(t *testing.T, on bool) *saasStack {
	t.Helper()
	s := &saasStack{saas: &fakeSaas{metered: map[string]bool{liveTenant.String(): true}}, limits: &saasLimits{allowed: true}, ingest: &saasIngest{}, billing: &saasBilling{}}
	gs, _ := grpcx.NewServer(grpcx.ServerOptions{Extra: []grpc.ServerOption{grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor())}})
	plansv1.RegisterSaasServiceServer(gs, s.saas)
	plansv1.RegisterAdminServiceServer(gs, saasAdmin{})
	limitsv1.RegisterLimitsServiceServer(gs, s.limits)
	ingestv1.RegisterIngestServiceServer(gs, s.ingest)
	billingv1.RegisterBillingServiceServer(gs, s.billing)
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
	s.product = newProduct(t, nil, func(d *Deps) {
		d.Limits, d.Ingest, d.Billing, d.Admin = limitsv1.NewLimitsServiceClient(cc), ingestv1.NewIngestServiceClient(cc), billingv1.NewBillingServiceClient(cc), plansv1.NewAdminServiceClient(cc)
		d.PublicURL = "https://api.example.test"
		d.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
		if on {
			d.Saas = plansv1.NewSaasServiceClient(cc)
		}
	})
	return s
}

func (s *saasStack) ingestEvents(t *testing.T, key string) (*http.Response, map[string]any) {
	t.Helper()
	body := `{"events":[{"idempotency_key":"k1","customer_external_id":"c","meter_code":"gb","quantity":1,"event_time":"2026-11-03T10:15:00Z"},{"idempotency_key":"k2","customer_external_id":"c","meter_code":"gb","quantity":2,"event_time":"2026-11-03T10:15:00Z"},{"idempotency_key":"k3","customer_external_id":"c","meter_code":"dup","quantity":2,"event_time":"2026-11-03T10:15:00Z"}]}`
	return s.call(t, "POST", "/v1/events", key, "", body)
}

func TestSaasOffMeansNothingExists(t *testing.T) {
	s := newSaasStack(t, false)
	resp, m := s.call(t, "GET", "/app/v1/account/plan", tokAda, "", "")
	if resp.StatusCode != 200 || m["enabled"] != false || m["quotas"] == nil {
		t.Fatalf("plan with SaaS off: %d %v", resp.StatusCode, m)
	}
	resp, m = s.call(t, "PUT", "/app/v1/account/plan", tokAda, "", `{"plan_code":"pro"}`)
	if resp.StatusCode != 409 || errKeyOf(m) != "plans.saas.disabled" {
		t.Fatalf("change with SaaS off: %d %v", resp.StatusCode, m)
	}
	if resp, m = s.call(t, "GET", "/admin/v1/saas", operatorToken, "", ""); resp.StatusCode != 200 || m["enabled"] != false {
		t.Fatalf("operator saas with SaaS off: %d %v", resp.StatusCode, m)
	}
	if resp, m = s.call(t, "GET", "/admin/v1/organizations", operatorToken, "", ""); resp.StatusCode != 409 || errKeyOf(m) != "plans.saas.disabled" {
		t.Fatalf("organisations with SaaS off: %d %v", resp.StatusCode, m)
	}
	// Ingest is untouched: nothing is asked of plans-svc, limits-svc, and the batch is forwarded once.
	if resp, _ = s.ingestEvents(t, "live-key"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest with SaaS off: %d", resp.StatusCode)
	}
	if len(s.ingest.calls) != 1 || s.saas.policyCalls != 0 || s.limits.asked != 0 {
		t.Errorf("with SaaS off: %d ingest calls, %d policy calls, %d quota reads; want 1, 0, 0", len(s.ingest.calls), s.saas.policyCalls, s.limits.asked)
	}
	// Every tenant stays in the operator's list.
	if _, m = s.call(t, "GET", "/admin/v1/tenants", operatorToken, "", ""); len(m["tenants"].([]any)) != 3 {
		t.Errorf("tenants = %v", m["tenants"])
	}
}

func TestPlanPageMergesTheEventsUsage(t *testing.T) {
	s := newSaasStack(t, true)
	resp, m := s.call(t, "GET", "/app/v1/account/plan", tokAda, "", "")
	if resp.StatusCode != 200 || m["enabled"] != true || m["can_manage"] != true {
		t.Fatalf("plan: %d %v", resp.StatusCode, m)
	}
	quotas := m["quotas"].([]any)
	events := quotas[0].(map[string]any)
	// Numbers, not strings: the console reads them as numbers.
	if events["resource"] != "events" || events["used"] != float64(123) || events["limit"] != float64(10000) || events["over_limit"] != false || events["resets_time"] != "2026-12-01T00:00:00Z" {
		t.Errorf("events quota = %v", events)
	}
	if apps := quotas[1].(map[string]any); apps["used"] != float64(1) || apps["over_limit"] != true {
		t.Errorf("apps quota = %v", apps)
	}
	// Limits-svc down: the page still answers, without the count.
	s.limits.mu.Lock()
	s.limits.err = status.Error(codes.Unavailable, "down")
	s.limits.mu.Unlock()
	if resp, m = s.call(t, "GET", "/app/v1/account/plan", tokAda, "", ""); resp.StatusCode != 200 || m["quotas"].([]any)[0].(map[string]any)["used"] != float64(0) {
		t.Errorf("plan with limits down: %d %v", resp.StatusCode, m)
	}
}

func TestChangePlanPassesTheCallerNotTheBody(t *testing.T) {
	s := newSaasStack(t, true)
	resp, m := s.call(t, "PUT", "/app/v1/account/plan", tokAda, "", `{"plan_code":"pro","account_id":"x","user_id":"y"}`)
	if resp.StatusCode != 200 || m["plan"].(map[string]any)["code"] != "pro" {
		t.Fatalf("change: %d %v", resp.StatusCode, m)
	}
	resp, m = s.call(t, "PUT", "/app/v1/account/plan", tokAda, "", `{"plan_code":"free"}`)
	if resp.StatusCode != 409 || errKeyOf(m) != "plans.saas.same_plan" {
		t.Errorf("same plan: %d %v", resp.StatusCode, m)
	}
	if resp, _ = s.call(t, "PUT", "/app/v1/account/plan", "", "", `{"plan_code":"pro"}`); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
}

func TestPlanInvoicesAndLinks(t *testing.T) {
	s := newSaasStack(t, true)
	resp, m := s.call(t, "GET", "/app/v1/account/plan/invoices", tokAda, "", "")
	invoices, _ := m["invoices"].([]any)
	if resp.StatusCode != 200 || len(invoices) != 1 || invoices[0].(map[string]any)["id"] != "i-open" || invoices[0].(map[string]any)["total_minor"] != float64(2900) {
		t.Fatalf("invoices (drafts and invoices of nothing are left out): %d %v", resp.StatusCode, m)
	}
	pay := "/app/v1/account/plan/invoices/0193c000-0000-7000-8000-0000000000a1/pay-link"
	resp, m = s.call(t, "POST", pay, tokAda, "", "")
	if resp.StatusCode != 201 || m["url"] != "https://api.example.test/i/tok123" {
		t.Fatalf("pay link: %d %v", resp.StatusCode, m)
	}
	if len(s.billing.links) != 1 || s.billing.links[0].GetKind() != "invoice" || s.billing.links[0].GetIdempotencyKey() == "" {
		t.Errorf("link request = %v", s.billing.links)
	}
	// Another customer's invoice is not found, an invoice that is not waiting for money is not payable, and a viewer may not make links at all.
	resp, m = s.call(t, "POST", "/app/v1/account/plan/invoices/0193c000-0000-7000-8000-0000000000a2/pay-link", tokAda, "", "")
	if resp.StatusCode != 404 || errKeyOf(m) != "billing.invoice.not_found" {
		t.Errorf("another customer's invoice: %d %v", resp.StatusCode, m)
	}
	resp, m = s.call(t, "POST", "/app/v1/account/plan/invoices/0193c000-0000-7000-8000-0000000000a3/pay-link", tokAda, "", "")
	if resp.StatusCode != 409 || errKeyOf(m) != "gateway.saas.invoice_not_payable" {
		t.Errorf("a paid invoice: %d %v", resp.StatusCode, m)
	}
	resp, m = s.call(t, "POST", pay, tokViewer, "", "")
	if resp.StatusCode != 403 || errKeyOf(m) != "identity.account.forbidden" {
		t.Errorf("a viewer making a link: %d %v", resp.StatusCode, m)
	}
	if resp, m = s.call(t, "GET", "/app/v1/account/plan/invoices", tokViewer, "", ""); resp.StatusCode != 200 {
		t.Errorf("a viewer reads the invoices: %d %v", resp.StatusCode, m)
	}
	resp, m = s.call(t, "POST", "/app/v1/account/plan/portal-link", tokAda, "", "")
	if resp.StatusCode != 201 || m["url"] != "https://api.example.test/c/ptok" {
		t.Errorf("portal link: %d %v", resp.StatusCode, m)
	}
}

func TestIngestIsLimitedAndMeteredForOrganisations(t *testing.T) {
	s := newSaasStack(t, true)
	resp, m := s.ingestEvents(t, "live-key")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest within the quota: %d %v", resp.StatusCode, m)
	}
	if len(s.ingest.calls) != 2 {
		t.Fatalf("ingest calls = %d, want the batch and its metering", len(s.ingest.calls))
	}
	metering := s.ingest.calls[1]
	ev := metering.req.GetEvents()[0]
	if metering.tenant.ID.String() != platformTenantID || ev.GetCustomerExternalId() != adaAcct || ev.GetMeterCode() != "events" {
		t.Errorf("metering went to %v for %q on %q", metering.tenant.ID, ev.GetCustomerExternalId(), ev.GetMeterCode())
	}
	// Two accepted, one duplicate: the duplicate is not counted, and the key derives from the first accepted event so a replay is not counted twice.
	if ev.GetQuantity() != "2" || ev.GetIdempotencyKey() != "saas-events:ev0" || ev.GetProperties()["tenant_id"] != liveTenant.String() {
		t.Errorf("metering event = %v", ev)
	}
}

func TestIngestOverTheHardQuotaIsRefused(t *testing.T) {
	s := newSaasStack(t, true)
	s.limits.allowed = false
	resp, m := s.ingestEvents(t, "live-key")
	if resp.StatusCode != http.StatusTooManyRequests || errKeyOf(m) != "gateway.saas.events_quota" {
		t.Fatalf("ingest over the quota: %d %v", resp.StatusCode, m)
	}
	if len(s.ingest.calls) != 0 {
		t.Error("events over the quota reached ingest-svc")
	}
}

func TestIngestOfSandboxAndUnknownPolicyIsNotLimited(t *testing.T) {
	s := newSaasStack(t, true)
	s.limits.allowed = false
	if resp, m := s.ingestEvents(t, "sandbox-key"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("a sandbox is free: %d %v", resp.StatusCode, m)
	}
	if len(s.ingest.calls) != 1 || s.limits.asked != 0 {
		t.Errorf("sandbox: %d ingest calls, %d quota reads; want 1 and 0", len(s.ingest.calls), s.limits.asked)
	}
	// plans-svc down: the policy is unknown, so the events are neither limited nor metered (limits must never take ingest down).
	s2 := newSaasStack(t, true)
	s2.limits.allowed = false
	s2.saas.policyErr = status.Error(codes.Unavailable, "down")
	if resp, m := s2.ingestEvents(t, "live-key"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("policy unavailable: %d %v", resp.StatusCode, m)
	}
	if len(s2.ingest.calls) != 1 {
		t.Errorf("ingest calls = %d, want 1", len(s2.ingest.calls))
	}
	// limits-svc down: the quota is unknown, so the events are allowed.
	s3 := newSaasStack(t, true)
	s3.limits.err = status.Error(codes.Unavailable, "down")
	if resp, m := s3.ingestEvents(t, "live-key"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("quota unavailable: %d %v", resp.StatusCode, m)
	}
}

func (f *fakeSaas) ListOrganizations(context.Context, *plansv1.ListOrganizationsRequest) (*plansv1.ListOrganizationsResponse, error) {
	return &plansv1.ListOrganizationsResponse{Platform: platformRef(), NextPageToken: "next", Organizations: []*plansv1.OrganizationRow{
		{AccountId: adaAcct, Name: "Ada Labs", OwnerEmail: "ada@example.com", PlanCode: "free", PlanName: "Free", State: "active", Apps: 1, EventsLimit: 10000, CustomerId: saasCustomer},
		{AccountId: "no-customer", Name: "New", PlanCode: "free", PlanName: "Free", State: "none"},
	}}, nil
}

func (f *fakeSaas) SetOrganizationPlan(ctx context.Context, req *plansv1.SetOrganizationPlanRequest) (*plansv1.SetOrganizationPlanResponse, error) {
	if !tenant.IsOperator(ctx) {
		return nil, errkey.New(codes.PermissionDenied, "identity.operator.forbidden", "operator only")
	}
	r, _ := f.GetMyPlan(ctx, &plansv1.GetMyPlanRequest{UserId: adaUser, AccountId: req.GetAccountId()})
	r.Plan.Plan = &plansv1.PlanOffer{Code: req.GetPlanCode(), Current: true}
	r.Plan.Overridden = len(req.GetLimits()) > 0
	return &plansv1.SetOrganizationPlanResponse{Plan: r.Plan}, nil
}

func (f *fakeSaas) ClearOrganizationOverrides(ctx context.Context, _ *plansv1.ClearOrganizationOverridesRequest) (*plansv1.ClearOrganizationOverridesResponse, error) {
	if !tenant.IsOperator(ctx) {
		return nil, errkey.New(codes.PermissionDenied, "identity.operator.forbidden", "operator only")
	}
	return &plansv1.ClearOrganizationOverridesResponse{}, nil
}

func TestOperatorViewOfOrganisations(t *testing.T) {
	s := newSaasStack(t, true)
	if resp, _ := s.call(t, "GET", "/admin/v1/organizations", tokAda, "", ""); resp.StatusCode != 403 {
		t.Errorf("a person who is not an operator: %d", resp.StatusCode)
	}
	resp, m := s.call(t, "GET", "/admin/v1/organizations?q=ada", operatorToken, "", "")
	rows, _ := m["organizations"].([]any)
	if resp.StatusCode != 200 || len(rows) != 2 || m["next_page_token"] != "next" || m["platform"].(map[string]any)["tenant_id"] != platformTenantID {
		t.Fatalf("organisations: %d %v", resp.StatusCode, m)
	}
	ada := rows[0].(map[string]any)
	if ada["events_used"] != float64(123) || ada["events_limit"] != float64(10000) || ada["apps"] != float64(1) || rows[1].(map[string]any)["events_used"] != float64(0) {
		t.Errorf("rows = %v", rows)
	}
	resp, m = s.call(t, "PUT", "/admin/v1/organizations/"+adaAcct+"/plan", operatorToken, "", `{"plan_code":"scale","limits":{"apps":0}}`)
	if resp.StatusCode != 200 || m["plan"].(map[string]any)["code"] != "scale" || m["overridden"] != true {
		t.Errorf("override: %d %v", resp.StatusCode, m)
	}
	if resp, _ = s.call(t, "DELETE", "/admin/v1/organizations/"+adaAcct+"/overrides", operatorToken, "", ""); resp.StatusCode != 204 {
		t.Errorf("clear: %d", resp.StatusCode)
	}
	resp, m = s.call(t, "GET", "/admin/v1/saas", operatorToken, "", "")
	if resp.StatusCode != 200 || m["enabled"] != true || m["tenant_id"] != platformTenantID || m["environment"] != "live" {
		t.Errorf("saas info: %d %v", resp.StatusCode, m)
	}
}

func TestOperatorTenantListLeavesThePlatformOut(t *testing.T) {
	s := newSaasStack(t, true)
	_, m := s.call(t, "GET", "/admin/v1/tenants", operatorToken, "", "")
	for _, x := range m["tenants"].([]any) {
		if x.(map[string]any)["id"] == platformTenantID {
			t.Error("the platform tenant is in the operator's tenant list")
		}
	}
	if len(m["tenants"].([]any)) != 2 {
		t.Errorf("tenants = %v", m["tenants"])
	}
}
