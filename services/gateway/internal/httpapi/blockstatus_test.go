package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (f *fakePlans) BlockCustomer(_ context.Context, req *plansv1.BlockCustomerRequest) (*plansv1.BlockCustomerResponse, error) {
	f.lastBlock = req
	return &plansv1.BlockCustomerResponse{Customer: &plansv1.Customer{ExternalId: req.GetExternalId(), BlockReason: req.GetReason(), BlockCategory: req.GetCategory(), BlockedUntil: req.GetBlockedUntil()}}, nil
}

func (f *fakePlans) UnblockCustomer(_ context.Context, req *plansv1.UnblockCustomerRequest) (*plansv1.UnblockCustomerResponse, error) {
	f.lastUnblock = req
	return &plansv1.UnblockCustomerResponse{Customer: &plansv1.Customer{ExternalId: req.GetExternalId()}}, nil
}

func (f *fakePlans) ListCustomerBlockEvents(context.Context, *plansv1.ListCustomerBlockEventsRequest) (*plansv1.ListCustomerBlockEventsResponse, error) {
	return &plansv1.ListCustomerBlockEventsResponse{Events: []*plansv1.CustomerBlockEvent{{Id: "e1", Action: "blocked", Category: "fraud", Actor: "ada@example.com", CreateTime: timestamppb.Now()}}}, nil
}

func (f *fakePlans) SetTenantStatus(_ context.Context, req *plansv1.SetTenantStatusRequest) (*plansv1.SetTenantStatusResponse, error) {
	f.lastStatus = req
	return &plansv1.SetTenantStatusResponse{Tenant: &plansv1.Tenant{Id: req.GetTenantId(), Status: req.GetStatus()}}, nil
}

func (f *fakePlans) ListTenantStatusEvents(context.Context, *plansv1.ListTenantStatusEventsRequest) (*plansv1.ListTenantStatusEventsResponse, error) {
	return &plansv1.ListTenantStatusEventsResponse{Events: []*plansv1.TenantStatusEvent{{Id: "t1", Kind: "changed", FromStatus: plansv1.TenantStatus_TENANT_STATUS_ACTIVE,
		ToStatus: plansv1.TenantStatus_TENANT_STATUS_FROZEN, Reason: "unpaid", Actor: "boss@example.com"}}}, nil
}

// A frozen tenant's key reads and writes nothing: every call that is not a GET or HEAD is refused with its own key, not the "suspended" one.
func TestAFrozenTenantsKeyIsReadOnly(t *testing.T) {
	ts, _ := newTestServer(t)
	if r, body := do(t, ts, http.MethodGet, "/v1/customers/c1/block-events", "frozen-key", ""); r.StatusCode != http.StatusOK {
		t.Errorf("a read: %d %v", r.StatusCode, body)
	}
	r, body := do(t, ts, http.MethodPost, "/v1/customers", "frozen-key", `{"external_id":"c9","name":"x","email":"x@example.com"}`)
	if r.StatusCode != http.StatusForbidden || errKey(body) != "gateway.auth.tenant_read_only" {
		t.Errorf("a write: %d %v", r.StatusCode, body)
	}
}

const frozenTenantID = "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d60"

// An operator (or a person) acting as a frozen tenant in the console reads and writes nothing: the tenant API answers a write with the read-only key, a read as ever.
func TestActingAsAFrozenTenantIsReadOnly(t *testing.T) {
	ts, _ := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+operatorToken)
		req.Header.Set(ActAsHeader, frozenTenantID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := call(http.MethodGet, "/v1/customers/c1/block-events", ""); code != http.StatusOK {
		t.Errorf("a read: %d %s", code, body)
	}
	if code, body := call(http.MethodPost, "/v1/customers/c1/block", `{}`); code != http.StatusForbidden || !strings.Contains(body, "gateway.auth.tenant_read_only") {
		t.Errorf("a write: %d %s", code, body)
	}
}

// Who blocks is the gateway's word, never the body's: an API key is "key:<id>", whatever the body says.
func TestBlockRecordsWhoBlockedAndCarriesTheCategoryAndTheEnd(t *testing.T) {
	ts, f := newTestServer(t)
	r, body := do(t, ts, http.MethodPost, "/v1/customers/c1/block", "keyed-key", `{"reason":"chargebacks","category":"chargeback","blocked_until":"2099-01-01T00:00:00Z","actor":"forged@example.com"}`)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("block: %d %v", r.StatusCode, body)
	}
	got := f.lastBlock
	if got.GetActor() != "key:key-123" || got.GetCategory() != "chargeback" || got.GetReason() != "chargebacks" || got.GetBlockedUntil().AsTime().Year() != 2099 || got.GetExternalId() != "c1" {
		t.Errorf("request = %v", got)
	}
	if r, body := do(t, ts, http.MethodPost, "/v1/customers/c1/unblock", "keyed-key", ""); r.StatusCode != http.StatusOK || f.lastUnblock.GetActor() != "key:key-123" {
		t.Errorf("unblock: %d %v %v", r.StatusCode, body, f.lastUnblock)
	}
	if r, body := do(t, ts, http.MethodGet, "/v1/customers/c1/block-events", "keyed-key", ""); r.StatusCode != http.StatusOK || body["events"] == nil {
		t.Errorf("block events: %d %v", r.StatusCode, body)
	}
}

// The operator sets a tenant frozen or banned with a category and a reason, recorded for the person the gateway knows; other words for a status are refused.
func TestOperatorSetsTenantStatusAndReadsItsHistory(t *testing.T) {
	ts, f := newTestServer(t)
	const id = "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e"
	for word, want := range map[string]plansv1.TenantStatus{"frozen": plansv1.TenantStatus_TENANT_STATUS_FROZEN, "banned": plansv1.TenantStatus_TENANT_STATUS_BANNED} {
		r, body := do(t, ts, http.MethodPut, "/admin/v1/tenants/"+id+"/status", operatorToken, `{"status":"`+word+`","category":"nonpayment","reason":"unpaid"}`)
		if r.StatusCode != http.StatusOK || f.lastStatus.GetStatus() != want || f.lastStatus.GetCategory() != "nonpayment" || f.lastStatus.GetReason() != "unpaid" || f.lastStatus.GetActor() != "operator" {
			t.Errorf("%s: %d %v %v", word, r.StatusCode, body, f.lastStatus)
		}
	}
	if r, body := do(t, ts, http.MethodPut, "/admin/v1/tenants/"+id+"/status", operatorToken, `{"status":"deleted"}`); r.StatusCode != http.StatusBadRequest || errKey(body) != "plans.tenant.bad_status" {
		t.Errorf("an unknown status: %d %v", r.StatusCode, body)
	}
	if r, body := do(t, ts, http.MethodGet, "/admin/v1/tenants/"+id+"/status-events", operatorToken, ""); r.StatusCode != http.StatusOK || body["events"] == nil {
		t.Errorf("status events: %d %v", r.StatusCode, body)
	}
}
