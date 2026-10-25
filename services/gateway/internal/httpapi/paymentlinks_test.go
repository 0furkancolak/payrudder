package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
)

// Scenarios: a token names what the fake billing-svc answers for it.
const (
	tokAdhoc       = "tok-adhoc-0123456789abcdefghijklmnopqrstuv"
	tokAdhocUsed   = "tok-adhoc-used-0123456789abcdefghijklmnopq"
	tokOpenSandbox = "tok-open-sandbox-0123456789abcdefghijklmn"
	tokOpenLive    = "tok-open-live-0123456789abcdefghijklmnopq"
	tokPack        = "tok-pack-0123456789abcdefghijklmnopqrstuvw"
	tokProcessing  = "tok-processing-0123456789abcdefghijklmnop"
	tokHostile     = "tok-hostile-0123456789abcdefghijklmnopqrs"
	portalTok      = "portal-0123456789abcdefghijklmnopqrstuvwxyz"
	liveTenantID   = "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e"
)

func openInvoice(env string, state string, payable bool) *billingv1.GetHostedInvoiceResponse {
	return &billingv1.GetHostedInvoiceResponse{
		Invoice:    &billingv1.Invoice{Id: "inv-open", Number: "INV-2026-000009", Currency: "USD", SubtotalMinor: 5000, TotalMinor: 5000, Status: "open", FinalizeTime: timestamppb.New(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)), Lines: []*billingv1.InvoiceLine{{Description: "Pro plan", AmountMinor: 5000}}},
		SellerName: "Acme", CustomerName: "Ada", CurrencyExponent: 2, Kind: "invoice", PaymentState: state, Payable: payable, Environment: env,
		Payment: &billingv1.HostedPaymentContext{TenantId: liveTenantID, InvoiceId: "inv-open", SagaId: "saga-1", Number: "INV-2026-000009", AmountMinor: 5000, Currency: "USD", Country: "DE", CustomerEmail: "ada@example.com", Environment: env},
	}
}

func scenarioInvoice(req *billingv1.GetHostedInvoiceRequest) (*billingv1.GetHostedInvoiceResponse, bool) {
	switch {
	case req.GetToken() == tokAdhoc:
		return &billingv1.GetHostedInvoiceResponse{Kind: "adhoc", SellerName: "Acme", Environment: "sandbox",
			Adhoc: &billingv1.HostedAdhoc{Description: "Consulting <b>day</b>", AmountMinor: 25000, Currency: "EUR", CurrencyExponent: 2}}, true
	case req.GetToken() == tokAdhocUsed:
		return &billingv1.GetHostedInvoiceResponse{Kind: "adhoc", SellerName: "Acme", Adhoc: &billingv1.HostedAdhoc{Description: "Once", AmountMinor: 100, Currency: "EUR", CurrencyExponent: 2, Consumed: true}}, true
	case req.GetToken() == tokPack:
		return &billingv1.GetHostedInvoiceResponse{Kind: "adhoc", SellerName: "Acme", Environment: "live",
			Adhoc: &billingv1.HostedAdhoc{Description: "Starter <i>pack</i>", AmountMinor: 1000, Currency: "USD", CurrencyExponent: 2,
				Pack: &billingv1.HostedCreditPack{Name: "Starter <i>pack</i>", Credits: 20000, Quantity: 2, ValidityDays: 30}}}, true
	case req.GetToken() == tokOpenSandbox:
		return openInvoice("sandbox", "open", true), true
	case req.GetToken() == tokOpenLive:
		return openInvoice("live", "open", true), true
	case req.GetToken() == tokProcessing:
		return openInvoice("live", "processing", true), true
	case req.GetToken() == tokHostile:
		r := openInvoice("live", "open", false)
		r.SellerName, r.CustomerName = `<img src=x onerror=alert(1)>`, `"><script>alert(2)</script>`
		r.Invoice.Lines[0].Description = `<script>alert(3)</script>`
		return r, true
	case req.GetPortalToken() == portalTok && req.GetInvoiceId() == "inv-open":
		return openInvoice("sandbox", "open", true), true
	}
	return nil, false
}

var (
	billingMu    sync.Mutex
	lastCreate   *billingv1.CreatePaymentLinkRequest
	lastPayment  *billingv1.RequestHostedPaymentRequest
	logoLocation string
)

func (fakeBilling) CreatePaymentLink(_ context.Context, req *billingv1.CreatePaymentLinkRequest) (*billingv1.CreatePaymentLinkResponse, error) {
	billingMu.Lock()
	lastCreate = req
	billingMu.Unlock()
	if req.GetAmountMinor() < 0 {
		return nil, status.Error(codes.InvalidArgument, "bad amount")
	}
	link := &billingv1.PaymentLink{Id: "link-1", Kind: "adhoc", Status: "active", AmountMinor: req.GetAmountMinor(), Currency: "EUR", IdempotencyKey: req.GetIdempotencyKey(), SingleUse: true}
	if req.GetIdempotencyKey() == "replay" {
		return &billingv1.CreatePaymentLinkResponse{Link: link, Replayed: true}, nil
	}
	return &billingv1.CreatePaymentLinkResponse{Link: link, Token: goodToken}, nil
}

func (fakeBilling) ListPaymentLinks(_ context.Context, req *billingv1.ListPaymentLinksRequest) (*billingv1.ListPaymentLinksResponse, error) {
	return &billingv1.ListPaymentLinksResponse{Links: []*billingv1.PaymentLink{{Id: "link-1", Kind: req.GetKind(), Status: req.GetStatus(), CustomerExternalId: req.GetCustomerExternalId()}}, NextPageToken: "n"}, nil
}

func (fakeBilling) GetPaymentLink(_ context.Context, req *billingv1.GetPaymentLinkRequest) (*billingv1.GetPaymentLinkResponse, error) {
	if req.GetLinkId() != "link-1" {
		return nil, status.Error(codes.NotFound, "no such link")
	}
	return &billingv1.GetPaymentLinkResponse{Link: &billingv1.PaymentLink{Id: "link-1", Status: "active"}}, nil
}

func (fakeBilling) RevokePaymentLink(_ context.Context, req *billingv1.RevokePaymentLinkRequest) (*billingv1.RevokePaymentLinkResponse, error) {
	return &billingv1.RevokePaymentLinkResponse{Link: &billingv1.PaymentLink{Id: req.GetLinkId(), Status: "revoked"}}, nil
}

func (fakeBilling) CreatePortalLink(_ context.Context, req *billingv1.CreatePortalLinkRequest) (*billingv1.CreatePortalLinkResponse, error) {
	if req.GetCustomerExternalId() == "nobody" {
		return nil, status.Error(codes.NotFound, "no such customer")
	}
	return &billingv1.CreatePortalLinkResponse{Token: portalTok, ExpireTime: timestamppb.New(time.Date(2027, 1, 20, 0, 0, 0, 0, time.UTC)), CustomerId: "cust-uuid"}, nil
}

func (fakeBilling) RevokePortalLinks(context.Context, *billingv1.RevokePortalLinksRequest) (*billingv1.RevokePortalLinksResponse, error) {
	return &billingv1.RevokePortalLinksResponse{Revoked: 1}, nil
}

func (fakeBilling) StartLinkPayment(_ context.Context, req *billingv1.StartLinkPaymentRequest) (*billingv1.StartLinkPaymentResponse, error) {
	switch req.GetToken() {
	case tokAdhoc:
		return &billingv1.StartLinkPaymentResponse{InvoiceToken: tokOpenSandbox}, nil
	case tokAdhocUsed:
		return nil, status.Error(codes.FailedPrecondition, "used")
	}
	return nil, status.Error(codes.NotFound, "no such link")
}

func (fakeBilling) RequestHostedPayment(_ context.Context, req *billingv1.RequestHostedPaymentRequest) (*billingv1.RequestHostedPaymentResponse, error) {
	billingMu.Lock()
	lastPayment = req
	billingMu.Unlock()
	switch {
	case req.GetToken() == tokOpenSandbox && req.GetMethod() == "mock", req.GetPortalToken() == portalTok && req.GetInvoiceId() == "inv-open" && req.GetMethod() == "mock":
		return &billingv1.RequestHostedPaymentResponse{Action: "started"}, nil
	case req.GetToken() == tokOpenLive:
		return &billingv1.RequestHostedPaymentResponse{Action: "provider", Payment: openInvoice("live", "open", true).GetPayment()}, nil
	case req.GetToken() == goodToken:
		return nil, status.Error(codes.FailedPrecondition, "not payable")
	}
	return nil, status.Error(codes.NotFound, "no such link")
}

func (fakeBilling) GetHostedLogoUrl(_ context.Context, req *billingv1.GetHostedLogoUrlRequest) (*billingv1.GetHostedLogoUrlResponse, error) {
	if req.GetToken() == goodToken || req.GetPortalToken() == portalTok {
		return &billingv1.GetHostedLogoUrlResponse{Url: logoLocation}, nil
	}
	return nil, status.Error(codes.NotFound, "no logo")
}

func (fakeBilling) GetPortal(_ context.Context, req *billingv1.GetPortalRequest) (*billingv1.GetPortalResponse, error) {
	if req.GetPortalToken() != portalTok {
		return nil, status.Error(codes.NotFound, "no such link")
	}
	return &billingv1.GetPortalResponse{CustomerName: "Ada", SellerName: "Acme <Ltd>", Environment: "live", TenantId: liveTenantID, CustomerId: "cust-uuid", LogoAvailable: true,
		Invoices: []*billingv1.PortalInvoice{
			{Id: "inv-open", Number: "INV-2026-000009", Status: "open", PaymentState: "open", Payable: true, TotalMinor: 5000, Currency: "USD", CurrencyExponent: 2, IssueTime: timestamppb.New(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))},
			{Id: "inv-paid", Number: "INV-2026-000003", Status: "paid", TotalMinor: 1500, Currency: "USD", CurrencyExponent: 2, IssueTime: timestamppb.New(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC))},
		},
		CreditBalance: 12500, CanBuyCredits: true,
		CreditLots: []*billingv1.PortalCreditLot{
			{Granted: 10000, Remaining: 2500, ExpireTime: timestamppb.New(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))},
			{Granted: 10000, Remaining: 10000},
		},
		CreditPacks: []*billingv1.PortalCreditPack{
			{Id: "pack-1", Name: "Starter <b>pack</b>", Credits: 10000, PriceMinor: 500, Currency: "USD", CurrencyExponent: 2, ValidityDays: 30},
			{Id: "pack-2", Name: "Team", Credits: 100000, PriceMinor: 4000, Currency: "USD", CurrencyExponent: 2},
		}}, nil
}

// fakeWorker is a payments-worker: it records what it is asked and answers from tables.
type fakeWorker struct {
	mu       sync.Mutex
	calls    []string
	bodies   []map[string]any
	tokens   []string
	rows     []map[string]any
	methods  []map[string]string
	pay      map[string]any
	payCode  int
	failList bool
}

func (f *fakeWorker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	f.tokens = append(f.tokens, r.Header.Get("X-Internal-Token"))
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.bodies = append(f.bodies, body)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/ledger/lookup"):
		if f.failList {
			http.Error(w, `{"error":"down"}`, http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": f.rows})
	case strings.HasSuffix(r.URL.Path, "/invoice-methods"):
		_ = json.NewEncoder(w).Encode(map[string]any{"methods": f.methods})
	case strings.HasSuffix(r.URL.Path, "/invoice-payments"):
		if f.payCode != 0 {
			w.WriteHeader(f.payCode)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(f.pay)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeWorker) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func withWorker(t *testing.T, f *fakeWorker, demo bool) *httptest.Server {
	t.Helper()
	pw := httptest.NewServer(f)
	t.Cleanup(pw.Close)
	u, _ := url.Parse(pw.URL)
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) {
		d.PaymentsURL, d.PaymentsToken, d.Demo = u, "internal-secret", demo
	})
	return ts
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func send(t *testing.T, c *http.Client, method, u string, form url.Values, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, u, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func get(t *testing.T, ts *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	return send(t, noRedirect(), http.MethodGet, ts.URL+path, nil, nil)
}

// --- the tenant API ---

func TestPaymentLinkAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	r, m := do(t, ts, http.MethodPost, "/v1/payment-links", "live-key", `{"kind":"adhoc","amount_minor":"2500","currency":"EUR","description":"Setup","customer_external_id":"cus_1","single_use":false,"expires_in_days":7}`)
	if r.StatusCode != http.StatusCreated || m["token"] != goodToken || m["url"] != "https://api.example.test/i/"+goodToken || m["id"] != "link-1" || m["amount_minor"] != "2500" || m["replayed"] != false {
		t.Fatalf("create = %d %v", r.StatusCode, m)
	}
	billingMu.Lock()
	got := lastCreate
	billingMu.Unlock()
	if got.GetKind() != "adhoc" || got.GetAmountMinor() != 2500 || got.GetCustomerExternalId() != "cus_1" || got.SingleUse == nil || got.GetSingleUse() || got.GetExpiresInDays() != 7 {
		t.Errorf("request = %v", got)
	}

	// The header is the idempotency key; a replay is a 200 without the token.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/v1/payment-links", strings.NewReader(`{"amount_minor":100}`))
	req.Header.Set("Authorization", "Bearer live-key")
	req.Header.Set("Idempotency-Key", "replay")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var rm map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&rm)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || rm["replayed"] != true || rm["token"] != "" || rm["url"] != "" || rm["idempotency_key"] != "replay" {
		t.Errorf("replay = %d %v", resp.StatusCode, rm)
	}

	if r, m := do(t, ts, http.MethodPost, "/v1/payment-links", "live-key", `{"amount_minor":-1}`); r.StatusCode != http.StatusBadRequest || errCode(m) != "INVALID_ARGUMENT" {
		t.Errorf("bad amount = %d %v", r.StatusCode, m)
	}
	if r, _ := do(t, ts, http.MethodPost, "/v1/payment-links", "", `{}`); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a key: %d", r.StatusCode)
	}

	r, m = do(t, ts, http.MethodGet, "/v1/payment-links?kind=adhoc&status=active&customer=cus_1", "live-key", "")
	links, _ := m["links"].([]any)
	if r.StatusCode != http.StatusOK || len(links) != 1 || links[0].(map[string]any)["kind"] != "adhoc" || links[0].(map[string]any)["customer_external_id"] != "cus_1" || m["next_page_token"] != "n" {
		t.Errorf("list = %d %v", r.StatusCode, m)
	}
	if r, m := do(t, ts, http.MethodGet, "/v1/payment-links?page_size=x", "live-key", ""); r.StatusCode != http.StatusBadRequest {
		t.Errorf("bad page size = %d %v", r.StatusCode, m)
	}
	if r, m := do(t, ts, http.MethodGet, "/v1/payment-links/link-1", "live-key", ""); r.StatusCode != http.StatusOK || m["status"] != "active" {
		t.Errorf("get = %d %v", r.StatusCode, m)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/payment-links/other", "live-key", ""); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown link = %d", r.StatusCode)
	}
	if r, m := do(t, ts, http.MethodDelete, "/v1/payment-links/link-1", "live-key", ""); r.StatusCode != http.StatusOK || m["status"] != "revoked" {
		t.Errorf("revoke = %d %v", r.StatusCode, m)
	}
}

func TestPortalLinkAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	r, m := do(t, ts, http.MethodPost, "/v1/customers/cus_1/portal-links", "live-key", `{"expires_in_days": 10}`)
	if r.StatusCode != http.StatusCreated || m["token"] != portalTok || m["url"] != "https://api.example.test/c/"+portalTok || m["expires_at"] != "2027-01-20T00:00:00Z" || m["customer_id"] != "cust-uuid" {
		t.Fatalf("create = %d %v", r.StatusCode, m)
	}
	if r, _ := do(t, ts, http.MethodPost, "/v1/customers/nobody/portal-links", "live-key", ""); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown customer = %d", r.StatusCode)
	}
	if r, m := do(t, ts, http.MethodDelete, "/v1/customers/cus_1/portal-links", "live-key", ""); r.StatusCode != http.StatusOK || m["revoked"] != float64(1) {
		t.Errorf("revoke = %d %v", r.StatusCode, m)
	}
}

func TestCustomerPayments(t *testing.T) {
	f := &fakeWorker{rows: []map[string]any{
		{"id": 1, "invoice_id": "inv-1", "provider": "mockpsp", "environment": "sandbox", "kind": "charge", "status": "succeeded", "amount_minor": 1116, "currency": "USD", "occurred_at": "2026-10-01T10:00:00Z"},
		{"id": 2, "invoice_id": "inv-1", "provider": "mockpsp", "environment": "sandbox", "kind": "refund", "status": "succeeded", "amount_minor": 116, "currency": "USD", "occurred_at": "2026-10-03T10:00:00Z"},
		{"id": 3, "invoice_id": "inv-1", "provider": "paddle", "environment": "live", "kind": "dispute", "status": "pending", "amount_minor": 1, "currency": "USD", "occurred_at": "2026-10-04T10:00:00Z"},
	}}
	ts := withWorker(t, f, false)
	r, m := do(t, ts, http.MethodGet, "/v1/customers/cus_1/payments?limit=50", "live-key", "")
	pays, _ := m["payments"].([]any)
	invs, _ := m["invoices"].([]any)
	if r.StatusCode != http.StatusOK || len(invs) != 1 || len(pays) != 2 {
		t.Fatalf("payments = %d %v", r.StatusCode, m)
	}
	first := pays[0].(map[string]any)
	if first["kind"] != "refund" || first["amount_minor"] != "116" || first["invoice_number"] != "INV-2026-000001" || first["occurred_at"] != "2026-10-03T10:00:00Z" {
		t.Errorf("newest first: %v", first)
	}
	calls := f.callList()
	if len(calls) != 1 || calls[0] != "POST /admin/tenants/"+liveTenantID+"/ledger/lookup" || f.tokens[0] != "internal-secret" {
		t.Errorf("calls = %v tokens %v", calls, f.tokens)
	}
	if ids, _ := f.bodies[0]["invoice_ids"].([]any); len(ids) != 1 || ids[0] != "inv-1" {
		t.Errorf("lookup ids = %v", f.bodies[0])
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/customers/cus_1/payments?limit=0", "live-key", ""); r.StatusCode != http.StatusBadRequest {
		t.Errorf("limit 0 = %d", r.StatusCode)
	}
	f.mu.Lock()
	f.failList = true
	f.mu.Unlock()
	if r, m := do(t, ts, http.MethodGet, "/v1/customers/cus_1/payments", "live-key", ""); r.StatusCode != http.StatusServiceUnavailable || errCode(m) != "UNAVAILABLE" {
		t.Errorf("worker down = %d %v", r.StatusCode, m)
	}
	// No payments-worker: the invoices alone.
	plain, _ := newTestServer(t)
	if r, m := do(t, plain, http.MethodGet, "/v1/customers/cus_1/payments", "live-key", ""); r.StatusCode != http.StatusOK || len(m["payments"].([]any)) != 0 {
		t.Errorf("no worker = %d %v", r.StatusCode, m)
	}
}

// --- the public pages ---

func TestAdhocLinkPage(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, page := get(t, ts, "/i/"+tokAdhoc)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{"Consulting &lt;b&gt;day&lt;/b&gt;", "250.00 EUR", `action="/i/` + tokAdhoc + `/start"`, "Continue", "Test mode"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<b>day</b>") {
		t.Error("the description was printed unescaped")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' https:") || !strings.Contains(csp, "img-src 'self'") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("csp = %q", csp)
	}
	if _, used := get(t, ts, "/i/"+tokAdhocUsed); !strings.Contains(used, "already been used") || strings.Contains(used, "/start") {
		t.Error("a consumed single-use link must say so and offer no Continue")
	}

	// Continue makes the invoice and goes on its page.
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhoc+"/start?lang=de", url.Values{}, nil)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/i/"+tokOpenSandbox+"?lang=de" {
		t.Errorf("start = %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokAdhocUsed+"/start", url.Values{}, nil); r.StatusCode != http.StatusConflict || !strings.Contains(body, "already been used") {
		t.Errorf("used start = %d", r.StatusCode)
	}
	if r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/nope/start", url.Values{}, nil); r.StatusCode != http.StatusNotFound || !strings.Contains(body, "does not exist or has expired") {
		t.Errorf("unknown start = %d", r.StatusCode)
	}
	// A GET never creates anything: the start is POST only.
	if r, _ := get(t, ts, "/i/"+tokAdhoc+"/start"); r.StatusCode == http.StatusOK || r.StatusCode == http.StatusSeeOther {
		t.Errorf("GET start = %d", r.StatusCode)
	}
}

func TestPayWithMockInSandbox(t *testing.T) {
	ts, _ := newTestServer(t)
	_, page := get(t, ts, "/i/"+tokOpenSandbox)
	for _, want := range []string{`action="/i/` + tokOpenSandbox + `/pay"`, `value="mock"`, "Pay (test)", "Awaiting payment", "Powered by Payrudder", "Test mode"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, `http-equiv="refresh"`) {
		t.Error("an open invoice must not refresh itself")
	}
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", url.Values{"method": {"mock"}}, nil)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/i/"+tokOpenSandbox {
		t.Fatalf("pay = %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	billingMu.Lock()
	if lastPayment.GetToken() != tokOpenSandbox || lastPayment.GetMethod() != "mock" {
		t.Errorf("billing was asked %v", lastPayment)
	}
	billingMu.Unlock()
	// Paid, unknown, or a missing method: no payment.
	if r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+goodToken+"/pay", url.Values{"method": {"mock"}}, nil); r.StatusCode != http.StatusConflict || !strings.Contains(body, "cannot be paid") {
		t.Errorf("paid invoice = %d", r.StatusCode)
	}
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/nope/pay", url.Values{"method": {"mock"}}, nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown = %d", r.StatusCode)
	}
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", url.Values{}, nil); r.StatusCode != http.StatusConflict {
		t.Errorf("no method = %d", r.StatusCode)
	}
}

func TestMockIsOfferedInDemoButNeverToALiveTenantOtherwise(t *testing.T) {
	f := &fakeWorker{methods: []map[string]string{{"id": "checkout", "kind": "checkout", "provider": "paddle"}}}
	live := withWorker(t, f, false)
	_, page := get(t, live, "/i/"+tokOpenLive)
	if strings.Contains(page, `value="mock"`) || !strings.Contains(page, `value="checkout"`) {
		t.Errorf("live page:\n%s", page)
	}
	demo := withWorker(t, &fakeWorker{}, true)
	if _, page := get(t, demo, "/i/"+tokOpenLive); !strings.Contains(page, `value="mock"`) || strings.Contains(page, `value="checkout"`) {
		t.Error("demo offers only the mock")
	}
}

func TestLivePaymentMethodsAndCheckout(t *testing.T) {
	f := &fakeWorker{
		methods: []map[string]string{{"id": "checkout", "kind": "checkout", "provider": "paddle"}, {"id": "instructions:banktransfer", "kind": "instructions", "provider": "banktransfer"}},
		pay:     map[string]any{"kind": "redirect", "url": "https://checkout.example/pay/abc"},
	}
	ts := withWorker(t, f, false)
	_, page := get(t, ts, "/i/"+tokOpenLive+"?lang=tr")
	for _, want := range []string{`value="checkout"`, "Kartla öde", `value="instructions:banktransfer"`, "Havale ile öde", `action="/i/` + tokOpenLive + `/pay?lang=tr"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	calls := f.callList()
	if !strings.Contains(strings.Join(calls, "|"), "GET /admin/tenants/"+liveTenantID+"/invoice-methods?country=DE&currency=USD&environment=live") {
		t.Errorf("calls = %v", calls)
	}

	// A checkout is a redirect to the provider; the return_url is this page.
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenLive+"/pay", url.Values{"method": {"checkout"}}, nil)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "https://checkout.example/pay/abc" {
		t.Fatalf("checkout = %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	f.mu.Lock()
	b := f.bodies[len(f.bodies)-1]
	last := f.calls[len(f.calls)-1]
	f.mu.Unlock()
	if last != "POST /admin/tenants/"+liveTenantID+"/invoice-payments" || b["invoice_id"] != "inv-open" || b["saga_id"] != "saga-1" || b["amount_minor"] != float64(5000) || b["method"] != "checkout" ||
		b["currency"] != "USD" || b["return_url"] != "https://api.example.test/i/"+tokOpenLive || b["customer_email"] != "ada@example.com" || b["environment"] != "live" {
		t.Errorf("payments-worker was asked %s %v", last, b)
	}

	// A redirect to anything but https is refused.
	f.mu.Lock()
	f.pay = map[string]any{"kind": "redirect", "url": "javascript:alert(1)"}
	f.mu.Unlock()
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenLive+"/pay", url.Values{"method": {"checkout"}}, nil); r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("javascript: url = %d", r.StatusCode)
	}

	// Instructions are a page with the reference.
	f.mu.Lock()
	f.pay = map[string]any{"kind": "instructions", "reference": "INV-2026-000009-X7", "lines": []map[string]string{{"label": "IBAN", "value": "DE00 <b>1234</b>"}}}
	f.mu.Unlock()
	resp, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenLive+"/pay", url.Values{"method": {"instructions:banktransfer"}}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("instructions = %d", resp.StatusCode)
	}
	for _, want := range []string{"INV-2026-000009-X7", "IBAN", "DE00 &lt;b&gt;1234&lt;/b&gt;", "50.00 USD"} {
		if !strings.Contains(body, want) {
			t.Errorf("instructions lack %q:\n%s", want, body)
		}
	}

	// payments-worker refusing is "cannot be paid", being down is "unavailable".
	f.mu.Lock()
	f.payCode = http.StatusConflict
	f.mu.Unlock()
	if r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenLive+"/pay", url.Values{"method": {"checkout"}}, nil); r.StatusCode != http.StatusConflict || !strings.Contains(body, "cannot be paid") {
		t.Errorf("worker refuses = %d", r.StatusCode)
	}
	plain, _ := newTestServer(t) // no payments-worker at all
	if r, body := send(t, noRedirect(), http.MethodPost, plain.URL+"/i/"+tokOpenLive+"/pay", url.Values{"method": {"checkout"}}, nil); r.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "unavailable") {
		t.Errorf("no worker = %d", r.StatusCode)
	}
	// Methods unavailable: a calm message, no buttons.
	if _, page := get(t, plain, "/i/"+tokOpenLive); !strings.Contains(page, "No payment method is available") || strings.Contains(page, "<button") {
		t.Error("expected the calm message")
	}
}

func TestPaymentStateDecidesTheRefresh(t *testing.T) {
	f := &fakeWorker{}
	ts := withWorker(t, f, false)
	_, page := get(t, ts, "/i/"+tokProcessing)
	if !strings.Contains(page, `<meta http-equiv="refresh" content="5">`) || !strings.Contains(page, "Payment processing") || strings.Contains(page, "<button") {
		t.Errorf("processing page:\n%s", page)
	}
	// Open at billing, a pending row in the ledger: processing. A declined latest row: failed, and it can be paid again.
	f.mu.Lock()
	f.methods = []map[string]string{{"id": "checkout", "kind": "checkout", "provider": "paddle"}}
	f.rows = []map[string]any{{"id": 7, "invoice_id": "inv-open", "provider": "paddle", "kind": "charge", "status": "pending", "amount_minor": 5000, "currency": "USD", "occurred_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}}
	f.mu.Unlock()
	if _, page := get(t, ts, "/i/"+tokOpenLive); !strings.Contains(page, `http-equiv="refresh"`) || !strings.Contains(page, "Payment processing") {
		t.Error("a pending ledger row means processing")
	}
	f.mu.Lock()
	f.rows = append(f.rows, map[string]any{"id": 8, "invoice_id": "inv-open", "provider": "paddle", "kind": "charge", "status": "declined", "amount_minor": 5000, "currency": "USD", "occurred_at": "2026-10-03T10:05:00Z"})
	f.mu.Unlock()
	_, page = get(t, ts, "/i/"+tokOpenLive)
	if strings.Contains(page, `http-equiv="refresh"`) || !strings.Contains(page, "Payment failed") || !strings.Contains(page, "You can try again") || !strings.Contains(page, `value="checkout"`) {
		t.Errorf("failed page:\n%s", page)
	}
	// An abandoned checkout (a pending row nothing ever closed) does not keep the page on "processing": the buttons come back.
	f.mu.Lock()
	f.rows = []map[string]any{{"id": 9, "invoice_id": "inv-open", "provider": "paddle", "kind": "charge", "status": "pending", "amount_minor": 5000, "currency": "USD", "occurred_at": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)}}
	f.mu.Unlock()
	if _, page := get(t, ts, "/i/"+tokOpenLive); strings.Contains(page, `http-equiv="refresh"`) || strings.Contains(page, "Payment processing") || !strings.Contains(page, `value="checkout"`) {
		t.Errorf("an abandoned checkout page:\n%s", page)
	}
	// The ledger never overrides a paid invoice.
	if _, paid := get(t, ts, "/i/"+goodToken); strings.Contains(paid, `http-equiv="refresh"`) || !strings.Contains(paid, "Paid") {
		t.Error("a paid invoice stays paid")
	}
}

func TestReceipt(t *testing.T) {
	f := &fakeWorker{rows: []map[string]any{{"id": 1, "invoice_id": "", "provider": "mockpsp", "kind": "charge", "status": "succeeded", "amount_minor": 1, "currency": "USD", "occurred_at": "2026-10-05T10:00:00Z"}}}
	ts := withWorker(t, f, false)
	resp, body := get(t, ts, "/i/"+goodToken+"/receipt")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Receipt · INV-2026-000007") || !strings.Contains(body, "Acme &amp; Sons") || !strings.Contains(body, "Paid") {
		t.Fatalf("receipt = %d\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "@media print") || strings.Contains(strings.ToLower(body), "<script") {
		t.Error("printable, no script")
	}
	_, page := get(t, ts, "/i/"+goodToken)
	if !strings.Contains(page, `href="/i/`+goodToken+`/receipt"`) {
		t.Error("a paid invoice links its receipt")
	}
	// An unpaid invoice has no receipt, nor does a stranger's token.
	for _, path := range []string{"/i/" + tokOpenSandbox + "/receipt", "/i/nope/receipt", "/i/" + tokAdhoc + "/receipt"} {
		if resp, _ := get(t, ts, path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d", path, resp.StatusCode)
		}
	}
}

func TestHostileTextIsEscaped(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, path := range []string{"/i/" + tokHostile, "/i/" + tokAdhoc} {
		resp, page := get(t, ts, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d", path, resp.StatusCode)
		}
		for _, bad := range []string{"<img src=x", "<script", "onerror=alert(1)>", "<b>day</b>"} {
			if strings.Contains(page, bad) {
				t.Errorf("%s printed %q unescaped", path, bad)
			}
		}
	}
	// No page carries a script, and the CSP loads nothing but styles and our own images.
	for _, path := range []string{"/i/" + tokOpenSandbox, "/i/" + goodToken, "/c/" + portalTok} {
		resp, page := get(t, ts, path)
		if strings.Contains(strings.ToLower(page), "<script") || strings.Contains(page, "onclick") {
			t.Errorf("%s has script", path)
		}
		if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") || resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s headers = %v", path, resp.Header)
		}
	}
}

func TestEveryNewStringIsTranslated(t *testing.T) {
	en := paymentStrings["en"]
	for _, lang := range hostedLangs {
		m, ok := paymentStrings[lang]
		if !ok {
			t.Fatalf("no strings for %s", lang)
		}
		for k, v := range en {
			if m[k] == "" {
				t.Errorf("%s lacks %q", lang, k)
			}
			if lang != "en" && m[k] == v && k != "st_paid" {
				// Brand-like words may coincide; a pure copy of the English is a missing translation.
				switch k {
				case "type", "method", "reference", "provider":
				default:
					t.Errorf("%s: %q is still English", lang, k)
				}
			}
		}
		for k := range m {
			if _, ok := en[k]; !ok {
				t.Errorf("%s has a key English lacks: %q", lang, k)
			}
		}
		// The phase 23 keys the pages still use.
		for _, k := range []string{"invoice", "from", "to", "date", "status", "description", "amount", "subtotal", "total", "pdf", "gone", "goneHint"} {
			if hostedText(lang)[k] == "" {
				t.Errorf("%s lacks %q", lang, k)
			}
		}
	}
	if hostedText("xx")["st_paid"] != "Paid" {
		t.Error("an unknown language falls back to English")
	}
}

func TestPayPostsAreRateLimitedMoreStrictly(t *testing.T) {
	ts, _ := newTestServer(t)
	codes := map[int]int{}
	for i := 0; i < hostedPaysPerMinute+3; i++ {
		r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", url.Values{"method": {"mock"}}, nil)
		codes[r.StatusCode]++
	}
	if codes[http.StatusSeeOther] != hostedPaysPerMinute || codes[http.StatusTooManyRequests] != 3 {
		t.Errorf("status counts = %v", codes)
	}
	// Reading a page is still allowed (60 a minute).
	if r, _ := get(t, ts, "/i/"+tokOpenSandbox); r.StatusCode != http.StatusOK {
		t.Errorf("page after the POST limit = %d", r.StatusCode)
	}
	var l hostedLimiter
	now := time.Now()
	for i := 0; i < 5; i++ {
		if !l.allowN("a", now, 5) {
			t.Fatalf("request %d refused", i+1)
		}
	}
	if l.allowN("a", now, 5) {
		t.Error("over the limit")
	}
}

// --- the portal ---

func TestPortalPages(t *testing.T) {
	f := &fakeWorker{rows: []map[string]any{
		{"id": 1, "invoice_id": "inv-paid", "provider": "paddle", "environment": "live", "kind": "charge", "status": "succeeded", "amount_minor": 1500, "currency": "USD", "occurred_at": "2026-09-02T10:00:00Z", "provider_ref": "txn_secret"},
		{"id": 2, "invoice_id": "inv-paid", "provider": "paddle", "environment": "live", "kind": "refund", "status": "succeeded", "amount_minor": 500, "currency": "USD", "occurred_at": "2026-09-09T10:00:00Z"},
		{"id": 3, "invoice_id": "inv-of-someone-else", "provider": "paddle", "environment": "live", "kind": "charge", "status": "succeeded", "amount_minor": 99999, "currency": "USD", "occurred_at": "2026-09-10T10:00:00Z"},
	}}
	ts := withWorker(t, f, false)
	resp, page := get(t, ts, "/c/"+portalTok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("portal = %d", resp.StatusCode)
	}
	for _, want := range []string{"Your invoices and payments", "Ada", "Acme &lt;Ltd&gt;", "INV-2026-000009", "INV-2026-000003", "50.00 USD", `href="/c/` + portalTok + `/invoices/inv-open"`, "Pay this invoice",
		"Refund", "Payment", "-5.00 USD", "15.00 USD", "paddle", "Succeeded", `src="/c/` + portalTok + `/logo"`, "2026-09-09"} {
		if !strings.Contains(page, want) {
			t.Errorf("portal lacks %q", want)
		}
	}
	for _, leak := range []string{"txn_secret", "999.99", "99999", "<Ltd>"} {
		if strings.Contains(page, leak) {
			t.Errorf("portal leaks %q", leak)
		}
	}
	if call := f.callList()[0]; call != "POST /admin/tenants/"+liveTenantID+"/ledger/lookup" {
		t.Errorf("call = %s", call)
	}
	if ids, _ := f.bodies[0]["invoice_ids"].([]any); len(ids) != 2 {
		t.Errorf("lookup was for %v: only the customer's invoices may be asked for", ids)
	}

	// The customer's invoice, in portal scope; billing is told the portal token and the invoice id.
	resp, page = get(t, ts, "/c/"+portalTok+"/invoices/inv-open?lang=fr")
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "Facture INV-2026-000009") || !strings.Contains(page, `action="/c/`+portalTok+`/invoices/inv-open/pay?lang=fr"`) || !strings.Contains(page, `href="/c/`+portalTok+`?lang=fr"`) {
		t.Errorf("invoice in portal = %d\n%s", resp.StatusCode, page)
	}
	// Any invoice the portal's customer does not own is the same 404 as a dead token: billing says NotFound and the page does not tell them apart.
	for _, path := range []string{"/c/nope", "/c/" + portalTok + "/invoices/inv-of-someone-else", "/c/nope/invoices/inv-open", "/c/" + portalTok + "/invoices/inv-of-someone-else/pdf",
		"/c/" + portalTok + "/invoices/inv-of-someone-else/receipt"} {
		resp, body := get(t, ts, path)
		if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "does not exist or has expired") {
			t.Errorf("%s = %d %q", path, resp.StatusCode, body)
		}
	}
	// A link token is not a portal token and the other way round.
	if resp, _ := get(t, ts, "/c/"+goodToken); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a link token opened the portal: %d", resp.StatusCode)
	}
	if resp, _ := get(t, ts, "/i/"+portalTok); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a portal token opened a link: %d", resp.StatusCode)
	}

	// Pay in portal scope.
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/invoices/inv-open/pay", url.Values{"method": {"mock"}}, nil)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/c/"+portalTok+"/invoices/inv-open" {
		t.Errorf("portal pay = %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	billingMu.Lock()
	if lastPayment.GetPortalToken() != portalTok || lastPayment.GetInvoiceId() != "inv-open" || lastPayment.GetToken() != "" {
		t.Errorf("billing was asked %v", lastPayment)
	}
	billingMu.Unlock()
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/invoices/inv-of-someone-else/pay", url.Values{"method": {"mock"}}, nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("pay for someone else's invoice = %d", r.StatusCode)
	}
}

// --- the logo ---

func TestLogoProxy(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 100))
	big := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxLogoBytes+10)...)
	mux := http.NewServeMux()
	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(png)
	})
	mux.HandleFunc("/big.png", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(big) })
	mux.HandleFunc("/evil.svg", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`))
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/logo.png", http.StatusFound) })
	store := httptest.NewServer(mux)
	t.Cleanup(store.Close)
	ts, _ := newTestServer(t)

	logoLocation = store.URL + "/logo.png"
	resp, body := get(t, ts, "/i/"+goodToken+"/logo")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || body != string(png) || resp.Header.Get("Cache-Control") != "private, max-age=300" {
		t.Errorf("logo = %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Cache-Control"))
	}
	if resp, _ := get(t, ts, "/c/"+portalTok+"/logo"); resp.StatusCode != http.StatusOK {
		t.Errorf("portal logo = %d", resp.StatusCode)
	}
	for _, loc := range []string{"/big.png", "/evil.svg", "/redirect", "/missing"} {
		logoLocation = store.URL + loc
		if resp, _ := get(t, ts, "/i/"+goodToken+"/logo"); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", loc, resp.StatusCode)
		}
	}
	logoLocation = "file:///etc/passwd"
	if resp, _ := get(t, ts, "/i/"+goodToken+"/logo"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("file url = %d", resp.StatusCode)
	}
	if resp, _ := get(t, ts, "/i/nope/logo"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown token logo = %d", resp.StatusCode)
	}
}

func TestLanguageFallbackOnTheNewPages(t *testing.T) {
	ts, _ := newTestServer(t)
	for lang, want := range map[string]string{"de": "Weiter", "es": "Continuar", "fr": "Continuer", "tr": "Devam", "xx": "Continue"} {
		if _, page := get(t, ts, "/i/"+tokAdhoc+"?lang="+lang); !strings.Contains(page, want) {
			t.Errorf("lang %s: %q missing", lang, want)
		}
	}
	_, page := get(t, ts, "/i/"+tokAdhoc+"?lang=ar")
	if !strings.Contains(page, `dir="rtl"`) || !strings.Contains(page, "متابعة") {
		t.Error("Arabic is right to left")
	}
	// ?lang= is kept on the form, but only a known language.
	if _, page := get(t, ts, "/i/"+tokAdhoc+"?lang=%22%3E%3Cx"); strings.Contains(page, "<x") {
		t.Error("a hostile lang parameter reached the page")
	}
}
