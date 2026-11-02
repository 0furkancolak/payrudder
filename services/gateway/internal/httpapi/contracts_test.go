package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// fakeContracts is plans-svc's ContractService as the gateway sees it: what the seller requires is set by the test, what the gateway sends is recorded.
type fakeContracts struct {
	plansv1.UnimplementedContractServiceServer
	mu         sync.Mutex
	enforced   bool
	satisfied  bool
	immediate  bool
	missing    []string
	reqErr     error
	recordErr  error
	required   []*plansv1.ContractRequirement
	asked      []*plansv1.GetContractRequirementsRequest
	snapshots  []*plansv1.GetContractSnapshotRequest // what was asked of GetContractSnapshot
	recorded   []*plansv1.RecordContractAcceptanceRequest
	recordedAs []string // the tenant each record was made for
	published  *plansv1.PublishContractVersionRequest
	withdrawal *plansv1.CreateContractWithdrawalRequest
	decided    *plansv1.SetContractWithdrawalStatusRequest
	// the follow-ups (withdrawal refund, the withdrawal function, the portal)
	settings      *plansv1.ContractSettings
	accepted      []*plansv1.ContractAcceptance
	listed        *plansv1.ListContractAcceptancesRequest
	nextPage      string
	withdrawals   []*plansv1.ContractWithdrawal
	claimErr      error
	reclaimed     bool
	claims        []string
	refunds       []*plansv1.RecordContractWithdrawalRefundRequest
	decideInvoice string
}

var lastContracts *fakeContracts

func newFakeContracts() *fakeContracts {
	return &fakeContracts{required: []*plansv1.ContractRequirement{
		{Kind: "distance_sales", VersionId: "ver-ds", VersionNumber: 3, Audience: "consumer", Locale: "en", BodyHtml: "<h3>Distance sales text</h3><p>Pays 50.00 USD</p>"},
		{Kind: "tos", VersionId: "ver-tos", VersionNumber: 1, Audience: "consumer", Locale: "en", BodyHtml: "<p>Terms text</p>"},
	}}
}

// enforce makes the seller require agreements the customer has not accepted.
func (f *fakeContracts) enforce(immediate bool) {
	f.mu.Lock()
	f.enforced, f.satisfied, f.immediate = true, false, immediate
	f.mu.Unlock()
}

func (f *fakeContracts) GetContractRequirements(ctx context.Context, req *plansv1.GetContractRequirementsRequest) (*plansv1.GetContractRequirementsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ti, ok := tenant.FromContext(ctx); !ok || ti.ID.String() != liveTenantID {
		return nil, status.Error(codes.PermissionDenied, "not the invoice's tenant")
	}
	f.asked = append(f.asked, req)
	if f.reqErr != nil {
		return nil, f.reqErr
	}
	if !f.enforced {
		return &plansv1.GetContractRequirementsResponse{Satisfied: true, Audience: "consumer"}, nil
	}
	out := &plansv1.GetContractRequirementsResponse{Enforced: true, Satisfied: f.satisfied, ImmediateStart: f.immediate, MissingKinds: f.missing, Audience: "consumer"}
	if f.satisfied {
		return out, nil
	}
	out.Requirements = append(out.Requirements, f.required...)
	return out, nil
}

func (f *fakeContracts) RecordContractAcceptance(ctx context.Context, req *plansv1.RecordContractAcceptanceRequest) (*plansv1.RecordContractAcceptanceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recordErr != nil {
		return nil, f.recordErr
	}
	ti, _ := tenant.FromContext(ctx)
	f.recorded = append(f.recorded, req)
	f.recordedAs = append(f.recordedAs, ti.ID.String())
	f.satisfied = true
	return &plansv1.RecordContractAcceptanceResponse{GroupId: "grp-1", Acceptances: []*plansv1.ContractAcceptance{{Id: "acc-1", Kind: "tos", Source: req.GetSource(), CustomerExternalId: req.GetCustomerExternalId()}}}, nil
}

func (f *fakeContracts) PublishContractVersion(_ context.Context, req *plansv1.PublishContractVersionRequest) (*plansv1.PublishContractVersionResponse, error) {
	f.mu.Lock()
	f.published = req
	f.mu.Unlock()
	return &plansv1.PublishContractVersionResponse{Version: &plansv1.ContractVersion{Id: "ver-1", Kind: req.GetKind(), Audience: "consumer", Number: 1, Material: true, Bodies: req.GetBodies(), Author: req.GetAuthor(), CreateTime: timestamppb.Now()}}, nil
}

func (f *fakeContracts) GetContractSnapshot(_ context.Context, req *plansv1.GetContractSnapshotRequest) (*plansv1.GetContractSnapshotResponse, error) {
	f.mu.Lock()
	f.snapshots = append(f.snapshots, req)
	f.mu.Unlock()
	if req.GetAcceptanceId() != "acc-1" && req.GetAcceptanceId() != "acc-ar" {
		return nil, errkey.New(codes.NotFound, "plans.contract.acceptance_not_found", "no such acceptance")
	}
	resp := &plansv1.GetContractSnapshotResponse{Html: "<html><body>the page</body></html>", Sha256: "abc123def456abc123"}
	if req.GetIncludePdf() {
		// acc-ar is a text the PDF font cannot draw.
		if req.GetAcceptanceId() == "acc-ar" {
			resp.PdfUnavailable = true
		} else {
			resp.Pdf = []byte("%PDF-1.3 the copy")
		}
	}
	return resp, nil
}

func (f *fakeContracts) SetContractSettings(_ context.Context, req *plansv1.SetContractSettingsRequest) (*plansv1.SetContractSettingsResponse, error) {
	return &plansv1.SetContractSettingsResponse{Settings: &plansv1.ContractSettings{RequiredKinds: req.GetRequiredKinds(), ImmediateStart: req.GetImmediateStart(), RetentionMonths: 36, PublishedKinds: []string{}}}, nil
}

func (f *fakeContracts) CreateContractWithdrawal(_ context.Context, req *plansv1.CreateContractWithdrawalRequest) (*plansv1.CreateContractWithdrawalResponse, error) {
	f.mu.Lock()
	f.withdrawal = req
	f.mu.Unlock()
	return &plansv1.CreateContractWithdrawalResponse{Withdrawal: &plansv1.ContractWithdrawal{Id: "wd-1", CustomerExternalId: req.GetCustomerExternalId(), Source: req.GetSource(), Status: "received", ReceiveTime: timestamppb.Now()}}, nil
}

func (f *fakeContracts) SetContractWithdrawalStatus(_ context.Context, req *plansv1.SetContractWithdrawalStatusRequest) (*plansv1.SetContractWithdrawalStatusResponse, error) {
	f.mu.Lock()
	f.decided = req
	f.mu.Unlock()
	return &plansv1.SetContractWithdrawalStatusResponse{Withdrawal: &plansv1.ContractWithdrawal{Id: req.GetId(), Status: req.GetStatus(), DecidedBy: req.GetDecidedBy(), InvoiceId: f.decideInvoice, ReceiveTime: timestamppb.Now()}}, nil
}

func (f *fakeContracts) ListContractVersions(context.Context, *plansv1.ListContractVersionsRequest) (*plansv1.ListContractVersionsResponse, error) {
	return &plansv1.ListContractVersionsResponse{}, nil
}

// --- the tenant API ---

func TestContractAPIPermissionsAndForcedFields(t *testing.T) {
	ts, _ := newTestServer(t)
	c := lastContracts

	// Managing the texts is contracts:write: a billing key (customers, subscriptions, invoices) cannot publish or change the settings, a read-only key can only read.
	if r, m := do(t, ts, http.MethodPost, "/v1/contracts/versions", "billing-key", `{"kind":"tos","bodies":{"en":"x"}}`); r.StatusCode != http.StatusForbidden {
		t.Errorf("a billing key publishing a contract: %d %v", r.StatusCode, m)
	}
	if r, _ := do(t, ts, http.MethodPut, "/v1/contracts/settings", "read-key", `{"required_kinds":["tos"]}`); r.StatusCode != http.StatusForbidden {
		t.Errorf("a read-only key changing the settings: %d", r.StatusCode)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/contracts/versions", "read-key", ``); r.StatusCode != http.StatusOK {
		t.Errorf("a read-only key reading the versions: %d", r.StatusCode)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/contracts/versions", "custom-key", ``); r.StatusCode != http.StatusForbidden {
		t.Errorf("a key without contracts:read: %d", r.StatusCode)
	}

	// Recording what a customer accepted is a customer record: the checkout's server needs customers:write only, and cannot choose the source.
	r, m := do(t, ts, http.MethodPost, "/v1/customers/cus_1/contract-acceptances", "billing-key",
		`{"customer_id":"hijack","source":"hosted","context_kind":"hosted_invoice","ip":"203.0.113.9","items":[{"version_id":"ver-tos","checkbox_text":"I accept"}]}`)
	if r.StatusCode != http.StatusCreated || m["group_id"] != "grp-1" {
		t.Fatalf("record: %d %v", r.StatusCode, m)
	}
	rec := c.recorded[len(c.recorded)-1]
	if rec.GetSource() != "api" || rec.GetContextKind() != "api" || rec.GetCustomerExternalId() != "cus_1" || rec.GetCustomerId() != "" || rec.GetIp() != "203.0.113.9" {
		t.Errorf("the body chose its own source or customer, or the seller's claim was lost: %+v", rec)
	}

	// The author of a version is the key that published it, whatever the key's holder writes in X-Payrudder-Actor or the body (TestContractAuthorIsWhoWasAuthenticated
	// has the other callers).
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/contracts/versions", strings.NewReader(`{"kind":"tos","bodies":{"en":"Terms"},"author":"someone else"}`))
	req.Header.Set("Authorization", "Bearer keyed-key")
	req.Header.Set("X-Payrudder-Actor", "owner@example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	if c.published.GetAuthor() != "api-key:key-123" || c.published.GetKind() != "tos" {
		t.Errorf("published = %+v", c.published)
	}

	// A withdrawal is "api" unless the console says so, and never "hosted"; the decider is recorded.
	do(t, ts, http.MethodPost, "/v1/customers/cus_1/contract-withdrawals", "live-key", `{"source":"hosted","statement":"no thanks"}`)
	if c.withdrawal.GetSource() != "api" || c.withdrawal.GetCustomerExternalId() != "cus_1" {
		t.Errorf("withdrawal = %+v", c.withdrawal)
	}
	do(t, ts, http.MethodPost, "/v1/customers/cus_1/contract-withdrawals", "live-key", `{"source":"console"}`)
	if c.withdrawal.GetSource() != "console" {
		t.Errorf("the console's withdrawal: %+v", c.withdrawal)
	}
	decide, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/contract-withdrawals/wd-1/status", strings.NewReader(`{"status":"accepted","note":"ok","decided_by":"someone else"}`))
	decide.Header.Set("Authorization", "Bearer keyed-key")
	decide.Header.Set("X-Payrudder-Actor", "owner@example.com")
	if resp, err := http.DefaultClient.Do(decide); err != nil || resp.StatusCode != http.StatusOK || c.decided.GetId() != "wd-1" || c.decided.GetDecidedBy() != "api-key:key-123" {
		t.Errorf("decide: %v %v %+v", resp, err, c.decided)
	}
}

// Who is recorded as the author of a version and the decider of a withdrawal comes from who the gateway authenticated: an API key is its own id, a signed-in person
// their verified address, the machine token the actor it declares. The X-Payrudder-Actor header and the author in the body are never taken from anybody who is
// not the machine token.
func TestContractAuthorIsWhoWasAuthenticated(t *testing.T) {
	p := newProduct(t, nil, nil)
	publish := func(token, tenantID string, hdr map[string]string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, p.ts.URL+"/v1/contracts/versions", strings.NewReader(`{"kind":"tos","bodies":{"en":"Terms"},"author":"body@example.com"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		if tenantID != "" {
			req.Header.Set(ActAsHeader, tenantID)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusCreated {
			t.Fatalf("publish as %s: %v %v", token, resp, err)
		}
		_ = resp.Body.Close()
		return p.contracts.published.GetAuthor()
	}
	decide := func(token, tenantID string, hdr map[string]string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPut, p.ts.URL+"/v1/contract-withdrawals/wd-1/status", strings.NewReader(`{"status":"declined","decided_by":"body@example.com"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		if tenantID != "" {
			req.Header.Set(ActAsHeader, tenantID)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("decide as %s: %v %v", token, resp, err)
		}
		_ = resp.Body.Close()
		return p.contracts.decided.GetDecidedBy()
	}
	spoof := map[string]string{"X-Payrudder-Actor": "ceo@victim.example"}
	for name, c := range map[string]struct {
		token, tenantID string
		hdr             map[string]string
		want            string
	}{
		"an API key, whatever it sends":                {"keyed-key", "", spoof, "api-key:key-123"},
		"an API key sending nothing":                   {"keyed-key", "", nil, "api-key:key-123"},
		"an API key that holds no id (never real)":     {"live-key", "", spoof, "api-key:"},
		"a signed-in owner, whatever the client sends": {tokAda, adaLive, spoof, "ada@example.com"},
		"a signed-in owner sending nothing":            {tokAda, adaLive, nil, "ada@example.com"},
		"the machine token declares who it is":         {operatorToken, liveTenant.String(), spoof, "ceo@victim.example"},
		"the machine token declaring nothing":          {operatorToken, liveTenant.String(), nil, "operator"},
	} {
		if got := publish(c.token, c.tenantID, c.hdr); got != c.want {
			t.Errorf("publish, %s: author %q, want %q", name, got, c.want)
		}
		if got := decide(c.token, c.tenantID, c.hdr); got != c.want {
			t.Errorf("decide, %s: decided_by %q, want %q", name, got, c.want)
		}
	}
	long := map[string]string{"X-Payrudder-Actor": strings.Repeat("é", 500)}
	if got := publish(operatorToken, liveTenant.String(), long); len([]rune(got)) != 120 {
		t.Errorf("the machine token's actor is %d characters, want it cut to 120", len([]rune(got)))
	}
}

func TestContractSnapshotIsReturnedWithItsHash(t *testing.T) {
	ts, _ := newTestServer(t)
	r, m := do(t, ts, http.MethodGet, "/v1/contract-acceptances/acc-1/snapshot", "live-key", ``)
	if r.StatusCode != 200 || m["sha256"] != "abc123def456abc123" || !strings.Contains(m["html"].(string), "the page") || r.Header.Get("Cache-Control") == "" {
		t.Errorf("snapshot: %d %v %v", r.StatusCode, m, r.Header)
	}
	if r, m := do(t, ts, http.MethodGet, "/v1/contract-acceptances/nope/snapshot", "live-key", ``); r.StatusCode != http.StatusNotFound || errCode(m) != "NOT_FOUND" {
		t.Errorf("an unknown acceptance: %d %v", r.StatusCode, m)
	}
}

// --- the hosted page ---

func contractsServer(t *testing.T) (*httptest.Server, *fakeContracts, *fakeWorker) {
	t.Helper()
	w := &fakeWorker{}
	ts := withWorker(t, w, false)
	return ts, lastContracts, w
}

func resetPayment() {
	billingMu.Lock()
	lastPayment = nil
	billingMu.Unlock()
}

func paid() *billingRequest {
	billingMu.Lock()
	defer billingMu.Unlock()
	if lastPayment == nil {
		return nil
	}
	return &billingRequest{method: lastPayment.GetMethod()}
}

type billingRequest struct{ method string }

func agreeForm(boxes ...string) url.Values {
	f := url.Values{"method": {"mock"}, "v_distance_sales": {"ver-ds"}, "v_tos": {"ver-tos"}}
	for _, b := range boxes {
		f.Set(b, "1")
	}
	return f
}

func TestHostedPageAsksForTheAgreementsNextToThePayButton(t *testing.T) {
	ts, c, _ := contractsServer(t)

	_, plain := get(t, ts, "/i/"+tokOpenSandbox)
	if strings.Contains(plain, `class="agree"`) || !strings.Contains(plain, `name="method"`) {
		t.Fatalf("a seller that requires nothing: the plain pay form:\n%s", plain)
	}
	c.enforce(false)
	_, page := get(t, ts, "/i/"+tokOpenSandbox)
	for _, want := range []string{`class="agree"`, "Distance sales text", "Terms text", `name="accept_distance_sales" value="1" required`, `name="v_tos" value="ver-tos"`, "I have read and accept the terms of service.",
		"Distance sales contract · version 3", "Order with obligation to pay", `<input type="hidden" name="method" value="mock">`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "consent_start") {
		t.Error("a seller that does not start at once does not ask for the consents")
	}
	if strings.Contains(page, "<script") || strings.Contains(page, "onclick") {
		t.Error("the page runs script: the pages run under default-src 'none'")
	}
	last := c.asked[len(c.asked)-1]
	if last.GetContextRef() != "inv-open" || last.GetCustomerId() != "" || last.GetLocale() != "en" || last.GetOrder().GetTotal() != "50.00" || last.GetOrder().GetCurrency() != "USD" {
		t.Errorf("the page asked for the wrong person or order: %+v", last)
	}

	_, tr := get(t, ts, "/i/"+tokOpenSandbox+"?lang=tr")
	for _, want := range []string{"Ödeme yükümlülüğü ile siparişi onayla", "Kullanım koşullarını okudum ve kabul ediyorum.", "Sözleşmeler"} {
		if !strings.Contains(tr, want) {
			t.Errorf("the Turkish page lacks %q", want)
		}
	}

	c.enforce(true)
	_, imm := get(t, ts, "/i/"+tokOpenSandbox)
	if !strings.Contains(imm, `name="consent_start" value="1" required`) || !strings.Contains(imm, `name="consent_waiver" value="1" required`) {
		t.Errorf("a consumer of a seller that starts at once gets two boxes of their own:\n%s", imm)
	}
}

func TestHostedPayStartsNothingWithoutEveryBox(t *testing.T) {
	ts, c, _ := contractsServer(t)
	c.enforce(false)
	post := func(f url.Values) (*http.Response, string) {
		resetPayment()
		return send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", f, map[string]string{"User-Agent": "test-agent/1.0"})
	}

	// No box, one box, a box that is not "1": the page again, nothing recorded, nothing paid. A form that does not name the version is not from this page.
	for name, f := range map[string]url.Values{
		"none":                  agreeForm(),
		"one of two":            agreeForm("accept_tos"),
		"the other one":         agreeForm("accept_distance_sales"),
		"boxes without version": {"method": {"mock"}, "accept_tos": {"1"}, "accept_distance_sales": {"1"}},
		"a box that is not 1":   {"method": {"mock"}, "v_tos": {"ver-tos"}, "accept_tos": {"on"}, "v_distance_sales": {"ver-ds"}, "accept_distance_sales": {"1"}},
	} {
		r, page := post(f)
		want := "Please read and tick every box below to continue."
		if name == "boxes without version" {
			want = "The text was updated while you were reading."
		}
		if r.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, want) || !strings.Contains(page, `class="agree"`) {
			t.Errorf("%s: %d\n%s", name, r.StatusCode, page)
		}
		if paid() != nil || len(c.recorded) != 0 {
			t.Fatalf("%s: a payment was started (%v) or an acceptance recorded (%d) without every box", name, paid(), len(c.recorded))
		}
	}

	// A text replaced while the customer read: the boxes are for the old version.
	f := agreeForm("accept_distance_sales", "accept_tos")
	f.Set("v_tos", "ver-tos-old")
	r, page := post(f)
	if r.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "The text was updated while you were reading.") || paid() != nil || len(c.recorded) != 0 {
		t.Errorf("a stale version: %d paid=%v recorded=%d\n%s", r.StatusCode, paid(), len(c.recorded), page)
	}

	// Every box: the acceptance is recorded with what the server saw, and the payment goes on.
	r, _ = post(agreeForm("accept_distance_sales", "accept_tos"))
	if r.StatusCode != http.StatusSeeOther || paid() == nil || paid().method != "mock" || len(c.recorded) != 1 {
		t.Fatalf("every box: %d paid=%v recorded=%d", r.StatusCode, paid(), len(c.recorded))
	}
	rec := c.recorded[0]
	if rec.GetSource() != "hosted" || rec.GetContextKind() != "hosted_invoice" || rec.GetContextRef() != "inv-open" || rec.GetUserAgent() != "test-agent/1.0" || rec.GetIp() == "" ||
		rec.GetLocale() != "en" || len(rec.GetItems()) != 2 || rec.GetItems()[0].GetVersionId() != "ver-ds" || rec.GetItems()[1].GetCheckboxText() != "I have read and accept the terms of service." ||
		rec.GetImmediateStartConsent() || c.recordedAs[0] != liveTenantID {
		t.Errorf("the evidence: %+v as tenant %s", rec, c.recordedAs)
	}

	// Accepted already: the form does not ask again, and the payment is not held up.
	resetPayment()
	r, _ = send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", url.Values{"method": {"mock"}}, nil)
	if r.StatusCode != http.StatusSeeOther || paid() == nil || len(c.recorded) != 1 {
		t.Errorf("accepted already: %d paid=%v recorded=%d", r.StatusCode, paid(), len(c.recorded))
	}
}

func TestHostedPayAsksForTheTwoConsentsSeparately(t *testing.T) {
	ts, c, _ := contractsServer(t)
	c.enforce(true)
	for name, f := range map[string]url.Values{
		"neither":            agreeForm("accept_distance_sales", "accept_tos"),
		"the request only":   agreeForm("accept_distance_sales", "accept_tos", "consent_start"),
		"the waiver only":    agreeForm("accept_distance_sales", "accept_tos", "consent_waiver"),
		"the consents alone": agreeForm("consent_start", "consent_waiver"),
	} {
		resetPayment()
		r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", f, nil)
		if r.StatusCode != http.StatusUnprocessableEntity || paid() != nil || len(c.recorded) != 0 {
			t.Errorf("%s: %d paid=%v recorded=%d", name, r.StatusCode, paid(), len(c.recorded))
		}
	}
	resetPayment()
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay?lang=tr", agreeForm("accept_distance_sales", "accept_tos", "consent_start", "consent_waiver"), nil)
	if r.StatusCode != http.StatusSeeOther || len(c.recorded) != 1 {
		t.Fatalf("both consents: %d recorded=%d", r.StatusCode, len(c.recorded))
	}
	rec := c.recorded[0]
	if !rec.GetImmediateStartConsent() || !rec.GetWithdrawalWaiverAcknowledged() || rec.GetLocale() != "tr" ||
		rec.GetImmediateStartText() != "14 günlük cayma süresi dolmadan hizmetin hemen başlamasını açıkça talep ediyorum." || !strings.Contains(rec.GetWithdrawalWaiverText(), "cayma hakkımı kaybedeceğimi") {
		t.Errorf("the consents are recorded with the words the customer saw, in their language: %+v", rec)
	}
}

func TestHostedPaymentIsNeverStartedWhenAgreementsCannotBeChecked(t *testing.T) {
	ts, c, _ := contractsServer(t)
	c.enforce(false)

	// plans-svc down: the page offers no way to pay, and a pay request is refused with a page to retry; nothing reaches billing.
	c.reqErr = status.Error(codes.Unavailable, "down")
	_, page := get(t, ts, "/i/"+tokOpenSandbox)
	if strings.Contains(page, "<button") || strings.Contains(page, `name="method"`) || !strings.Contains(page, "This payment cannot be made right now.") {
		t.Errorf("the page offers a payment while the agreements cannot be checked:\n%s", page)
	}
	resetPayment()
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", url.Values{"method": {"mock"}}, nil)
	if r.StatusCode != http.StatusServiceUnavailable || paid() != nil {
		t.Errorf("pay with the agreements unreadable: %d paid=%v", r.StatusCode, paid())
	}

	// A seller who requires a kind it has not published: the same.
	c.reqErr = nil
	c.mu.Lock()
	c.missing = []string{"privacy"}
	c.mu.Unlock()
	resetPayment()
	r, _ = send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", agreeForm("accept_distance_sales", "accept_tos"), nil)
	if r.StatusCode != http.StatusServiceUnavailable || paid() != nil || len(c.recorded) != 0 {
		t.Errorf("pay with a required kind unpublished: %d paid=%v recorded=%d", r.StatusCode, paid(), len(c.recorded))
	}

	// The record cannot be written: no payment either.
	c.mu.Lock()
	c.missing = nil
	c.recordErr = status.Error(codes.Internal, "boom")
	c.mu.Unlock()
	resetPayment()
	r, _ = send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", agreeForm("accept_distance_sales", "accept_tos"), nil)
	if r.StatusCode != http.StatusServiceUnavailable || paid() != nil {
		t.Errorf("pay when the acceptance cannot be recorded: %d paid=%v", r.StatusCode, paid())
	}

	// plans-svc says a newer text exists: the page is shown again.
	c.mu.Lock()
	c.recordErr = errkey.New(codes.FailedPrecondition, "plans.contract.version_stale", "stale")
	c.mu.Unlock()
	resetPayment()
	r, page = send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", agreeForm("accept_distance_sales", "accept_tos"), nil)
	if r.StatusCode != http.StatusUnprocessableEntity || paid() != nil || !strings.Contains(page, "The text was updated while you were reading.") {
		t.Errorf("a version replaced between the page and the post: %d paid=%v", r.StatusCode, paid())
	}
}

func TestPortalInvoicePayIsGatedToo(t *testing.T) {
	ts, c, _ := contractsServer(t)
	c.enforce(false)
	path := ts.URL + "/c/" + portalTok + "/invoices/inv-open/pay"
	resetPayment()
	r, _ := send(t, noRedirect(), http.MethodPost, path, url.Values{"method": {"mock"}}, nil)
	if r.StatusCode != http.StatusUnprocessableEntity || paid() != nil {
		t.Fatalf("a portal payment without the boxes: %d paid=%v", r.StatusCode, paid())
	}
	r, _ = send(t, noRedirect(), http.MethodPost, path, agreeForm("accept_distance_sales", "accept_tos"), nil)
	if r.StatusCode != http.StatusSeeOther || paid() == nil || len(c.recorded) != 1 {
		t.Errorf("a portal payment with the boxes: %d paid=%v recorded=%d", r.StatusCode, paid(), len(c.recorded))
	}
	c.enforce(false)
	_, page := get(t, ts, "/c/"+portalTok+"/invoices/inv-open")
	if !strings.Contains(page, "Terms text") || !strings.Contains(page, `action="/c/`+portalTok+`/invoices/inv-open/pay"`) {
		t.Errorf("the portal's invoice page does not ask for the agreements:\n%s", page)
	}
}

func TestContractStringsAreTranslated(t *testing.T) {
	en := contractStrings["en"]
	for _, lang := range hostedLangs {
		m, ok := contractStrings[lang]
		if !ok {
			t.Fatalf("no contract strings for %s", lang)
		}
		for k, v := range en {
			if m[k] == "" {
				t.Errorf("%s lacks %q", lang, k)
			}
			if lang != "en" && m[k] == v && k != "contract_version" {
				t.Errorf("%s: %q is still English", lang, k)
			}
		}
		for k := range m {
			if _, ok := en[k]; !ok {
				t.Errorf("%s has a key English lacks: %q", lang, k)
			}
		}
	}
}
