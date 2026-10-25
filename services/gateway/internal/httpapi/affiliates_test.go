package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// fakeAffiliates stands in for plans-svc's AffiliateService and AffiliatePublicService; it keeps the last request of each call.
type fakeAffiliates struct {
	plansv1.UnimplementedAffiliateServiceServer
	plansv1.UnimplementedAffiliatePublicServiceServer
	mu   sync.Mutex
	last map[string]proto.Message
}

func (f *fakeAffiliates) remember(name string, m proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		f.last = map[string]proto.Message{}
	}
	f.last[name] = m
}

func (f *fakeAffiliates) got(name string) proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last[name]
}

func (f *fakeAffiliates) GetAffiliateProgram(context.Context, *plansv1.GetAffiliateProgramRequest) (*plansv1.GetAffiliateProgramResponse, error) {
	return nil, errkey.New(codes.NotFound, "affiliate.program.not_set", "no program")
}

func (f *fakeAffiliates) SetAffiliateProgram(_ context.Context, r *plansv1.SetAffiliateProgramRequest) (*plansv1.SetAffiliateProgramResponse, error) {
	f.remember("SetAffiliateProgram", r)
	return &plansv1.SetAffiliateProgramResponse{Program: r.GetProgram()}, nil
}

func (f *fakeAffiliates) CreateAffiliate(_ context.Context, r *plansv1.CreateAffiliateRequest) (*plansv1.CreateAffiliateResponse, error) {
	f.remember("CreateAffiliate", r)
	return &plansv1.CreateAffiliateResponse{Affiliate: &plansv1.Affiliate{Id: "a1", Code: "ANN", Email: r.GetEmail(), Status: "active"}}, nil
}

func (f *fakeAffiliates) ListAffiliates(_ context.Context, r *plansv1.ListAffiliatesRequest) (*plansv1.ListAffiliatesResponse, error) {
	f.remember("ListAffiliates", r)
	return &plansv1.ListAffiliatesResponse{Total: 1, Affiliates: []*plansv1.Affiliate{{Id: "a1", Code: "ANN", Clicks: 7}}}, nil
}

func (f *fakeAffiliates) UpdateAffiliate(_ context.Context, r *plansv1.UpdateAffiliateRequest) (*plansv1.UpdateAffiliateResponse, error) {
	f.remember("UpdateAffiliate", r)
	return &plansv1.UpdateAffiliateResponse{Affiliate: &plansv1.Affiliate{Id: r.GetId(), Status: r.GetStatus()}}, nil
}

func (f *fakeAffiliates) AttributeCustomer(_ context.Context, r *plansv1.AttributeCustomerRequest) (*plansv1.AttributeCustomerResponse, error) {
	f.remember("AttributeCustomer", r)
	return &plansv1.AttributeCustomerResponse{Attributed: true}, nil
}

func (f *fakeAffiliates) ListAffiliateReferrals(_ context.Context, r *plansv1.ListAffiliateReferralsRequest) (*plansv1.ListAffiliateReferralsResponse, error) {
	f.remember("ListAffiliateReferrals", r)
	return &plansv1.ListAffiliateReferralsResponse{}, nil
}

// lastAffiliates is the fake of the server newTestServerWith built last (the tests of this package do not run in parallel).
var lastAffiliates *fakeAffiliates

func (f *fakeAffiliates) ListAffiliateCommissions(_ context.Context, r *plansv1.ListAffiliateCommissionsRequest) (*plansv1.ListAffiliateCommissionsResponse, error) {
	f.remember("ListAffiliateCommissions", r)
	return &plansv1.ListAffiliateCommissionsResponse{Total: 1, Commissions: []*plansv1.AffiliateCommission{{Id: "c1", AmountMinor: 1000, Status: "pending"}}}, nil
}

func (f *fakeAffiliates) ReverseAffiliateCommission(_ context.Context, r *plansv1.ReverseAffiliateCommissionRequest) (*plansv1.ReverseAffiliateCommissionResponse, error) {
	f.remember("ReverseAffiliateCommission", r)
	return &plansv1.ReverseAffiliateCommissionResponse{ReversedMinor: r.GetAmountMinor()}, nil
}

func (f *fakeAffiliates) GetAffiliateBalances(_ context.Context, r *plansv1.GetAffiliateBalancesRequest) (*plansv1.GetAffiliateBalancesResponse, error) {
	f.remember("GetAffiliateBalances", r)
	return &plansv1.GetAffiliateBalancesResponse{Balances: []*plansv1.AffiliateBalance{{AffiliateId: "a1", Currency: "EUR", ApprovedMinor: 4200}}}, nil
}

func (f *fakeAffiliates) GetAffiliateFunnel(_ context.Context, r *plansv1.GetAffiliateFunnelRequest) (*plansv1.GetAffiliateFunnelResponse, error) {
	f.remember("GetAffiliateFunnel", r)
	return &plansv1.GetAffiliateFunnelResponse{Clicks: 9, Referrals: 3}, nil
}

func (f *fakeAffiliates) ListAffiliatePayouts(_ context.Context, r *plansv1.ListAffiliatePayoutsRequest) (*plansv1.ListAffiliatePayoutsResponse, error) {
	f.remember("ListAffiliatePayouts", r)
	return &plansv1.ListAffiliatePayoutsResponse{Total: 1, Payouts: []*plansv1.AffiliatePayout{{Id: "p1", Status: "open", TotalMinor: 3000, Fake: true}}}, nil
}

func (f *fakeAffiliates) CreateAffiliatePayouts(_ context.Context, r *plansv1.CreateAffiliatePayoutsRequest) (*plansv1.CreateAffiliatePayoutsResponse, error) {
	f.remember("CreateAffiliatePayouts", r)
	return &plansv1.CreateAffiliatePayoutsResponse{Payouts: []*plansv1.AffiliatePayout{{Id: "p1"}}, Skipped: []*plansv1.SkippedAffiliatePayout{{Currency: "TRY", Reason: "below_minimum"}}}, nil
}

func (f *fakeAffiliates) MarkAffiliatePayoutPaid(_ context.Context, r *plansv1.MarkAffiliatePayoutPaidRequest) (*plansv1.MarkAffiliatePayoutPaidResponse, error) {
	f.remember("MarkAffiliatePayoutPaid", r)
	return &plansv1.MarkAffiliatePayoutPaidResponse{Payout: &plansv1.AffiliatePayout{Id: r.GetId(), Status: "paid", Reference: r.GetReference()}}, nil
}

func (f *fakeAffiliates) CancelAffiliatePayout(_ context.Context, r *plansv1.CancelAffiliatePayoutRequest) (*plansv1.CancelAffiliatePayoutResponse, error) {
	f.remember("CancelAffiliatePayout", r)
	return &plansv1.CancelAffiliatePayoutResponse{Payout: &plansv1.AffiliatePayout{Id: r.GetId(), Status: "cancelled"}}, nil
}

func (f *fakeAffiliates) ListAffiliateJournal(_ context.Context, r *plansv1.ListAffiliateJournalRequest) (*plansv1.ListAffiliateJournalResponse, error) {
	f.remember("ListAffiliateJournal", r)
	return &plansv1.ListAffiliateJournalResponse{
		Entries: []*plansv1.AffiliateJournalEntry{
			{Id: "j1", PayoutId: "p1", Currency: "EUR", Account: "commission_expense", DebitMinor: 500, PostTime: timestamppb.New(time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)), Memo: "=cmd"},
			{Id: "j2", PayoutId: "p1", Currency: "EUR", Account: "commissions_payable", CreditMinor: 500, PostTime: timestamppb.New(time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)), Fake: true},
		},
		Totals: []*plansv1.AffiliateJournalTotal{{Currency: "EUR", DebitMinor: 500, CreditMinor: 500}},
	}, nil
}

func TestAffiliatePayoutRoutes(t *testing.T) {
	ts, _ := newTestServer(t)
	fa := lastAffiliates
	if r, m := do(t, ts, http.MethodGet, "/v1/affiliates/payouts?status=open&affiliate_id=a1", "live-key", ""); r.StatusCode != http.StatusOK || m["total"] != float64(1) {
		t.Fatalf("list: %d %v", r.StatusCode, m)
	}
	if l := fa.got("ListAffiliatePayouts").(*plansv1.ListAffiliatePayoutsRequest); l.GetStatus() != "open" || l.GetAffiliateId() != "a1" {
		t.Errorf("list request: %v", l)
	}
	if r, m := do(t, ts, http.MethodPost, "/v1/affiliates/payouts", "live-key", `{"affiliate_id":"a1","currency":"TRY"}`); r.StatusCode != http.StatusCreated || m["skipped"] == nil {
		t.Errorf("create: %d %v", r.StatusCode, m)
	}
	if r, _ := do(t, ts, http.MethodPost, "/v1/affiliates/payouts", "live-key", ``); r.StatusCode != http.StatusCreated {
		t.Errorf("create with no body (every partner): %d", r.StatusCode)
	}
	if r, m := do(t, ts, http.MethodPost, "/v1/affiliates/payouts/real-id/pay", "live-key", `{"id":"forged","reference":"TR99"}`); r.StatusCode != http.StatusOK || m["status"] != "paid" {
		t.Errorf("pay: %d %v", r.StatusCode, m)
	}
	if m := fa.got("MarkAffiliatePayoutPaid").(*plansv1.MarkAffiliatePayoutPaidRequest); m.GetId() != "real-id" || m.GetReference() != "TR99" {
		t.Errorf("pay request: %v", m)
	}
	if r, m := do(t, ts, http.MethodPost, "/v1/affiliates/payouts/p9/cancel", "live-key", ""); r.StatusCode != http.StatusOK || m["status"] != "cancelled" {
		t.Errorf("cancel: %d %v", r.StatusCode, m)
	}
	if r, m := do(t, ts, http.MethodGet, "/v1/affiliates/journal?payout_id=p1&currency=EUR&from=2026-03-01T00:00:00Z", "live-key", ""); r.StatusCode != http.StatusOK || m["totals"] == nil {
		t.Errorf("journal: %d %v", r.StatusCode, m)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/affiliates/journal?to=soon", "live-key", ""); r.StatusCode != http.StatusBadRequest {
		t.Errorf("a bad time = %d", r.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/affiliates/journal.csv?currency=EUR", nil)
	req.Header.Set("Authorization", "Bearer live-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{"posted_at,payout_id,affiliate_id,currency,account,debit_minor,credit_minor,fake,memo", "2026-03-01T10:00:00Z,p1,,EUR,commission_expense,500,0,false,'=cmd",
		"commissions_payable,0,500,true", "total,,,EUR,,500,500,,"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("csv lacks %q:\n%s", want, body)
		}
	}
}

func TestAffiliateCommissionRoutes(t *testing.T) {
	ts, _ := newTestServer(t)
	fa := lastAffiliates
	if r, m := do(t, ts, http.MethodGet, "/v1/affiliates/commissions?status=pending&affiliate_id=a1&kind=commission&invoice_id=i1&payout_id=p1&from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z&page_size=2", "live-key", ""); r.StatusCode != http.StatusOK || m["total"] != float64(1) {
		t.Fatalf("list: %d %v", r.StatusCode, m)
	}
	l := fa.got("ListAffiliateCommissions").(*plansv1.ListAffiliateCommissionsRequest)
	if l.GetStatus() != "pending" || l.GetAffiliateId() != "a1" || l.GetKind() != "commission" || l.GetInvoiceId() != "i1" || l.GetPayoutId() != "p1" || l.GetPageSize() != 2 ||
		l.GetFromTime().AsTime().Year() != 2026 || l.GetToTime().AsTime().Month() != 2 {
		t.Errorf("list request: %v", l)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/affiliates/commissions?from=yesterday", "live-key", ""); r.StatusCode != http.StatusBadRequest {
		t.Errorf("a bad time = %d", r.StatusCode)
	}
	if r, m := do(t, ts, http.MethodPost, "/v1/affiliates/commissions/reverse", "live-key", `{"invoice_id":"i1","reason":"chargeback","amount_minor":"300"}`); r.StatusCode != http.StatusOK || m["reversed_minor"] != "300" {
		t.Errorf("reverse: %d %v", r.StatusCode, m)
	}
	if r, m := do(t, ts, http.MethodGet, "/v1/affiliates/balances?affiliate_id=a1", "live-key", ""); r.StatusCode != http.StatusOK || m["balances"] == nil {
		t.Errorf("balances: %d %v", r.StatusCode, m)
	}
	if r, m := do(t, ts, http.MethodGet, "/v1/affiliates/funnel?from=2026-01-01&to=2026-01-31", "live-key", ""); r.StatusCode != http.StatusOK || m["clicks"] != "9" {
		t.Errorf("funnel: %d %v", r.StatusCode, m)
	}
	if f := fa.got("GetAffiliateFunnel").(*plansv1.GetAffiliateFunnelRequest); f.GetFromDate() != "2026-01-01" || f.GetToDate() != "2026-01-31" {
		t.Errorf("funnel request: %v", f)
	}
}

func TestAffiliateRoutes(t *testing.T) {
	ts, _ := newTestServer(t)
	fa := lastAffiliates

	if r, m := do(t, ts, http.MethodGet, "/v1/affiliates/program", "live-key", ""); r.StatusCode != http.StatusNotFound || errKey(m) != "affiliate.program.not_set" {
		t.Errorf("no program: %d %v", r.StatusCode, m)
	}
	if r, m := do(t, ts, http.MethodPut, "/v1/affiliates/program", "live-key", `{"status":"active","commission_kind":"percent_bps","commission_value":"1000","hold_days":30}`); r.StatusCode != http.StatusOK || m["commission_value"] != "1000" {
		t.Errorf("set program: %d %v", r.StatusCode, m)
	}
	if p := fa.got("SetAffiliateProgram").(*plansv1.SetAffiliateProgramRequest).GetProgram(); p.GetCommissionValue() != 1000 || p.GetHoldDays() != 30 {
		t.Errorf("program as sent to plans-svc: %v", p)
	}
	if r, m := do(t, ts, http.MethodPost, "/v1/affiliates", "live-key", `{"email":"ann@partners.test","name":"Ann"}`); r.StatusCode != http.StatusCreated || m["code"] != "ANN" {
		t.Errorf("create: %d %v", r.StatusCode, m)
	}
	if r, m := do(t, ts, http.MethodGet, "/v1/affiliates?status=pending&search=an&page_size=5&page_token=10", "live-key", ""); r.StatusCode != http.StatusOK || m["total"] != float64(1) {
		t.Errorf("list: %d %v", r.StatusCode, m)
	}
	if l := fa.got("ListAffiliates").(*plansv1.ListAffiliatesRequest); l.GetStatus() != "pending" || l.GetSearch() != "an" || l.GetPageSize() != 5 || l.GetPageToken() != "10" {
		t.Errorf("list request: %v", l)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/affiliates?page_size=x", "live-key", ""); r.StatusCode != http.StatusBadRequest {
		t.Errorf("bad page size = %d", r.StatusCode)
	}
	// The id is the path's, whatever the body says.
	if r, _ := do(t, ts, http.MethodPatch, "/v1/affiliates/real-id", "live-key", `{"id":"forged","status":"suspended"}`); r.StatusCode != http.StatusOK {
		t.Errorf("patch = %d", r.StatusCode)
	}
	if u := fa.got("UpdateAffiliate").(*plansv1.UpdateAffiliateRequest); u.GetId() != "real-id" || u.GetStatus() != "suspended" {
		t.Errorf("update request: %v", u)
	}
	// An attribution names the customer by the path; the internal id of a customer is not for a caller.
	if r, m := do(t, ts, http.MethodPost, "/v1/customers/cus_1/referral", "live-key", `{"code":"ANN","source":"link","customer_id":"11111111-1111-1111-1111-111111111111"}`); r.StatusCode != http.StatusOK || m["attributed"] != true {
		t.Errorf("attribute: %d %v", r.StatusCode, m)
	}
	if a := fa.got("AttributeCustomer").(*plansv1.AttributeCustomerRequest); a.GetCustomerExternalId() != "cus_1" || a.GetCustomerId() != "" || a.GetCode() != "ANN" {
		t.Errorf("attribute request: %v", a)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/affiliates/referrals?affiliate_id=a1", "live-key", ""); r.StatusCode != http.StatusOK {
		t.Errorf("referrals = %d", r.StatusCode)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/affiliates/program", "", ""); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a key = %d", r.StatusCode)
	}
}
