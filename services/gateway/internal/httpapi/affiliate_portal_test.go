package httpapi

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// fakePortal stands in for plans-svc's AffiliatePortalService; it keeps the last request of each call.
type fakePortal struct {
	plansv1.UnimplementedAffiliatePortalServiceServer
	mu   sync.Mutex
	last map[string]proto.Message
}

func (f *fakePortal) remember(name string, m proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		f.last = map[string]proto.Message{}
	}
	f.last[name] = m
}

func (f *fakePortal) got(name string) proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last[name]
}

var portalAffiliation = &plansv1.MyAffiliation{Id: "aff-1", TenantId: "tenant-1", Environment: "live", SellerName: "Acme", Code: "ANN", Status: "active"}

func (f *fakePortal) ListMyAffiliations(_ context.Context, r *plansv1.ListMyAffiliationsRequest) (*plansv1.ListMyAffiliationsResponse, error) {
	f.remember("ListMyAffiliations", r)
	return &plansv1.ListMyAffiliationsResponse{Affiliations: []*plansv1.MyAffiliation{proto.Clone(portalAffiliation).(*plansv1.MyAffiliation)}}, nil
}

func (f *fakePortal) ClaimMyAffiliations(_ context.Context, r *plansv1.ClaimMyAffiliationsRequest) (*plansv1.ClaimMyAffiliationsResponse, error) {
	f.remember("ClaimMyAffiliations", r)
	return &plansv1.ClaimMyAffiliationsResponse{Claimed: 1, Affiliations: []*plansv1.MyAffiliation{proto.Clone(portalAffiliation).(*plansv1.MyAffiliation)}}, nil
}

func (f *fakePortal) LookupAffiliateProgram(_ context.Context, r *plansv1.LookupAffiliateProgramRequest) (*plansv1.LookupAffiliateProgramResponse, error) {
	f.remember("LookupAffiliateProgram", r)
	if r.GetTenantId() != "tenant-1" {
		return nil, errkey.New(codes.NotFound, "affiliate.portal.program_unavailable", "none")
	}
	return &plansv1.LookupAffiliateProgramResponse{SellerName: "Acme", Environment: "live", Program: &plansv1.MyProgram{Status: "active", CommissionKind: "percent_bps", CommissionValue: 1000}}, nil
}

func (f *fakePortal) ApplyAsAffiliate(_ context.Context, r *plansv1.ApplyAsAffiliateRequest) (*plansv1.ApplyAsAffiliateResponse, error) {
	f.remember("ApplyAsAffiliate", r)
	return &plansv1.ApplyAsAffiliateResponse{Affiliation: &plansv1.MyAffiliation{Id: "aff-2", Code: "NEWCODE", Status: "pending"}}, nil
}

func (f *fakePortal) GetMyAffiliation(_ context.Context, r *plansv1.GetMyAffiliationRequest) (*plansv1.GetMyAffiliationResponse, error) {
	f.remember("GetMyAffiliation", r)
	if r.GetAffiliateId() != "aff-1" {
		return nil, errkey.New(codes.NotFound, "affiliate.not_found", "none")
	}
	return &plansv1.GetMyAffiliationResponse{Affiliation: proto.Clone(portalAffiliation).(*plansv1.MyAffiliation), Clicks: 12, Referrals: 3}, nil
}

func (f *fakePortal) UpdateMyAffiliation(_ context.Context, r *plansv1.UpdateMyAffiliationRequest) (*plansv1.UpdateMyAffiliationResponse, error) {
	f.remember("UpdateMyAffiliation", r)
	return &plansv1.UpdateMyAffiliationResponse{Affiliation: &plansv1.MyAffiliation{Id: r.GetAffiliateId(), Code: "ANN", PayoutDetails: r.GetPayoutDetails()}}, nil
}

func (f *fakePortal) ListMyReferrals(_ context.Context, r *plansv1.ListMyReferralsRequest) (*plansv1.ListMyReferralsResponse, error) {
	f.remember("ListMyReferrals", r)
	return &plansv1.ListMyReferralsResponse{Total: 1, Referrals: []*plansv1.MyReferral{{CustomerEmailMasked: "j***@example.com"}}}, nil
}

func (f *fakePortal) ListMyCommissions(_ context.Context, r *plansv1.ListMyCommissionsRequest) (*plansv1.ListMyCommissionsResponse, error) {
	f.remember("ListMyCommissions", r)
	return &plansv1.ListMyCommissionsResponse{Total: 1}, nil
}

func (f *fakePortal) ListMyPayouts(_ context.Context, r *plansv1.ListMyPayoutsRequest) (*plansv1.ListMyPayoutsResponse, error) {
	f.remember("ListMyPayouts", r)
	return &plansv1.ListMyPayoutsResponse{Total: 1, Payouts: []*plansv1.MyPayout{{Id: "p1", Fake: true}}}, nil
}

// lastPortal is the fake of the product server newProduct built last.
var lastPortal *fakePortal

func TestAffiliatePortalRoutes(t *testing.T) {
	p := newProduct(t, nil, nil)
	fp := lastPortal

	// Every call is for the person of the session, and the link is the gateway's.
	resp, m := p.call(t, "GET", "/app/v1/affiliate/affiliations", tokAda, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d %v", resp.StatusCode, m)
	}
	as, _ := m["affiliations"].([]any)
	if len(as) != 1 || as[0].(map[string]any)["link"] != "/r/ANN" {
		t.Errorf("affiliations = %v", m)
	}
	if got := fp.got("ListMyAffiliations").(*plansv1.ListMyAffiliationsRequest); got.GetUserId() != adaUser {
		t.Errorf("the user is %q: it must be the session's", got.GetUserId())
	}
	if resp, _ := p.call(t, "GET", "/app/v1/affiliate/affiliations", "", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a session = %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "GET", "/app/v1/affiliate/affiliations", "qk_live_notasession", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("with an API key instead of a session = %d", resp.StatusCode)
	}

	if resp, m := p.call(t, "POST", "/app/v1/affiliate/claim", tokAda, "", `{"user_id":"someone-else"}`); resp.StatusCode != http.StatusOK || m["claimed"] != float64(1) {
		t.Errorf("claim = %d %v", resp.StatusCode, m)
	}
	if got := fp.got("ClaimMyAffiliations").(*plansv1.ClaimMyAffiliationsRequest); got.GetUserId() != adaUser {
		t.Errorf("a body named the user: %q", got.GetUserId())
	}

	if resp, m := p.call(t, "GET", "/app/v1/affiliate/programs/tenant-1", tokAda, "", ""); resp.StatusCode != http.StatusOK || m["seller_name"] != "Acme" {
		t.Errorf("program = %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "GET", "/app/v1/affiliate/programs/other", tokAda, "", ""); resp.StatusCode != http.StatusNotFound || errKeyOf(m) != "affiliate.portal.program_unavailable" {
		t.Errorf("unknown program = %d %v", resp.StatusCode, m)
	}

	resp, m = p.call(t, "POST", "/app/v1/affiliate/applications", tokAda, "", `{"user_id":"x","tenant_id":"tenant-1","name":"Ann","payout_method":"manual","payout_details":"cash"}`)
	if resp.StatusCode != http.StatusCreated || m["link"] != "/r/NEWCODE" {
		t.Errorf("apply = %d %v", resp.StatusCode, m)
	}
	if got := fp.got("ApplyAsAffiliate").(*plansv1.ApplyAsAffiliateRequest); got.GetUserId() != adaUser || got.GetTenantId() != "tenant-1" || got.GetName() != "Ann" {
		t.Errorf("apply request = %v", got)
	}

	resp, m = p.call(t, "GET", "/app/v1/affiliate/affiliations/aff-1?from=2026-01-01&to=2026-01-31", tokAda, "", "")
	if resp.StatusCode != http.StatusOK || m["clicks"] != "12" {
		t.Errorf("get = %d %v", resp.StatusCode, m)
	}
	if got := fp.got("GetMyAffiliation").(*plansv1.GetMyAffiliationRequest); got.GetUserId() != adaUser || got.GetAffiliateId() != "aff-1" || got.GetFromDate() != "2026-01-01" || got.GetToDate() != "2026-01-31" {
		t.Errorf("get request = %v", got)
	}
	if resp, m := p.call(t, "GET", "/app/v1/affiliate/affiliations/not-mine", tokBob, "", ""); resp.StatusCode != http.StatusNotFound || errKeyOf(m) != "affiliate.not_found" {
		t.Errorf("another partner's affiliation = %d %v", resp.StatusCode, m)
	}

	resp, _ = p.call(t, "PATCH", "/app/v1/affiliate/affiliations/aff-1", tokAda, "", `{"affiliate_id":"forged","user_id":"forged","payout_method":"manual","payout_details":"new"}`)
	if got := fp.got("UpdateMyAffiliation").(*plansv1.UpdateMyAffiliationRequest); resp.StatusCode != http.StatusOK || got.GetAffiliateId() != "aff-1" || got.GetUserId() != adaUser || got.GetPayoutDetails() != "new" {
		t.Errorf("update = %d %v", resp.StatusCode, got)
	}

	for path, call := range map[string]string{"referrals": "ListMyReferrals", "commissions?status=paid&page_size=5&page_token=10": "ListMyCommissions", "payouts": "ListMyPayouts"} {
		if resp, _ := p.call(t, "GET", "/app/v1/affiliate/affiliations/aff-1/"+path, tokAda, "", ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s = %d", path, resp.StatusCode)
		}
		if fp.got(call) == nil {
			t.Errorf("%s was not asked", call)
		}
	}
	if c := fp.got("ListMyCommissions").(*plansv1.ListMyCommissionsRequest); c.GetStatus() != "paid" || c.GetPageSize() != 5 || c.GetPageToken() != "10" || c.GetUserId() != adaUser {
		t.Errorf("commissions request = %v", c)
	}
	if resp, _ := p.call(t, "GET", "/app/v1/affiliate/affiliations/aff-1/payouts?page_size=x", tokAda, "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad page size = %d", resp.StatusCode)
	}
}
