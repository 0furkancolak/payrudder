package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func (f *fakePlans) SubmitCustomerBlockAppeal(_ context.Context, req *plansv1.SubmitCustomerBlockAppealRequest) (*plansv1.SubmitCustomerBlockAppealResponse, error) {
	f.lastAppeal = req
	if f.appealError != nil {
		return nil, f.appealError
	}
	return &plansv1.SubmitCustomerBlockAppealResponse{Appeal: &plansv1.CustomerBlockAppeal{Id: "ap-1", Status: "open", Statement: req.GetStatement()}}, nil
}

func (f *fakePlans) ListCustomerBlockAppeals(context.Context, *plansv1.ListCustomerBlockAppealsRequest) (*plansv1.ListCustomerBlockAppealsResponse, error) {
	return &plansv1.ListCustomerBlockAppealsResponse{Appeals: f.appeals}, nil
}

func (f *fakePlans) DecideCustomerBlockAppeal(_ context.Context, req *plansv1.DecideCustomerBlockAppealRequest) (*plansv1.DecideCustomerBlockAppealResponse, error) {
	f.lastDecide = req
	return &plansv1.DecideCustomerBlockAppealResponse{Appeal: &plansv1.CustomerBlockAppeal{Id: req.GetAppealId(), Status: req.GetDecision(), Answer: req.GetAnswer(), DecidedBy: req.GetActor()}}, nil
}

// A blocked customer finds the appeal on the portal and the form behind it; neither says why they are blocked. A customer who is not blocked, or whose block has ended,
// is shown neither the link nor the form.
func TestABlockedCustomerCanReachTheAppealFormAndItNeverSaysWhy(t *testing.T) {
	ts, plans := newTestServer(t)
	plans.customer = &plansv1.Customer{Id: "cust-uuid", ExternalId: "ada-ext", Name: "Ada Lovelace", Locale: "en", BlockTime: timestamppb.Now(), BlockReason: "SELLER-NOTE-XYZ", BlockCategory: "fraud"}
	_, portal := get(t, ts, "/c/"+portalTok)
	if !strings.Contains(portal, `href="/c/`+portalTok+`/appeal"`) || strings.Contains(portal, "SELLER-NOTE-XYZ") || strings.Contains(portal, "fraud") {
		t.Errorf("the portal of a blocked customer:\n%s", portal)
	}
	resp, form := get(t, ts, "/c/"+portalTok+"/appeal")
	for _, want := range []string{"Appeal a restriction", `name="statement"`, `maxlength="2000"`, `action="/c/` + portalTok + `/appeal"`} {
		if !strings.Contains(form, want) {
			t.Errorf("the form lacks %q:\n%s", want, form)
		}
	}
	if resp.StatusCode != http.StatusOK || strings.Contains(form, "SELLER-NOTE-XYZ") || strings.Contains(form, "fraud") || resp.Header.Get("Cache-Control") != "no-store" ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action 'self'") {
		t.Errorf("the form: %d %v\n%s", resp.StatusCode, resp.Header, form)
	}

	// The block ended by itself (blocked_until in the past), and a customer who was never blocked: no link, and the form is not there.
	for name, c := range map[string]*plansv1.Customer{
		"ended":   {Id: "cust-uuid", ExternalId: "ada-ext", BlockTime: timestamppb.Now(), BlockedUntil: timestamppb.New(timestamppb.Now().AsTime().Add(-1))},
		"unblock": {Id: "cust-uuid", ExternalId: "ada-ext"},
	} {
		plans.customer = c
		if _, p := get(t, ts, "/c/"+portalTok); strings.Contains(p, "/appeal") {
			t.Errorf("%s: the portal links the appeal", name)
		}
		if r, p := get(t, ts, "/c/"+portalTok+"/appeal"); r.StatusCode != http.StatusNotFound || strings.Contains(p, `name="statement"`) {
			t.Errorf("%s: the form is offered: %d", name, r.StatusCode)
		}
		if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/appeal", url.Values{"statement": {"hello"}}, nil); r.StatusCode != http.StatusNotFound || plans.lastAppeal != nil {
			t.Errorf("%s: an appeal was posted: %d", name, r.StatusCode)
		}
	}
	// A token that opens nothing is the same gone page as everywhere.
	if r, _ := get(t, ts, "/c/no-such-token-no-such-token-no-such-token/appeal"); r.StatusCode != http.StatusNotFound {
		t.Errorf("a token that opens nothing: %d", r.StatusCode)
	}
}

// The statement goes to plans-svc as typed for the customer the token names (never one the form names), and everything the page prints back is escaped. The answers of
// plans-svc are words, not errors: an appeal that is open already, a block that ended, too long or empty, plans-svc down.
func TestAnAppealIsPostedForTheCustomerOfTheTokenAndTheAnswersAreWords(t *testing.T) {
	ts, plans := newTestServer(t)
	plans.customer = &plansv1.Customer{Id: "cust-uuid", ExternalId: "ada-ext", Locale: "en", BlockTime: timestamppb.Now()}
	post := func(f url.Values) (*http.Response, string) {
		return send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/appeal", f, nil)
	}
	resp, page := post(url.Values{"statement": {"It was <b>me</b>\nline two"}, "external_id": {"someone-else"}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "Appeal sent") || plans.lastAppeal.GetExternalId() != "ada-ext" || plans.lastAppeal.GetStatement() != "It was <b>me</b>\nline two" {
		t.Errorf("posting: %d %v\n%s", resp.StatusCode, plans.lastAppeal, page)
	}

	plans.appealError = errkey.New(codes.InvalidArgument, "plans.common.invalid", "statement: required")
	resp, page = post(url.Values{"statement": {"<script>alert(1)</script>"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || strings.Contains(page, "<script>alert") || !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") || !strings.Contains(page, "up to 2000 characters") {
		t.Errorf("an invalid statement is given back escaped, with the reason: %d\n%s", resp.StatusCode, page)
	}
	plans.appealError = errkey.New(codes.AlreadyExists, "plans.customer_appeal.exists", "open")
	if resp, page = post(url.Values{"statement": {"again"}}); resp.StatusCode != http.StatusOK || !strings.Contains(page, "has not answered yet") {
		t.Errorf("a second appeal: %d\n%s", resp.StatusCode, page)
	}
	plans.appealError = errkey.New(codes.FailedPrecondition, "plans.customer_appeal.not_blocked", "no")
	if resp, _ = post(url.Values{"statement": {"late"}}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a block that ended while the form was open: %d", resp.StatusCode)
	}
	plans.appealError = errkey.New(codes.Unavailable, "plans.down", "down")
	if resp, page = post(url.Values{"statement": {"down"}}); resp.StatusCode != http.StatusServiceUnavailable || strings.Contains(page, "plans.down") {
		t.Errorf("plans-svc down: %d\n%s", resp.StatusCode, page)
	}
}

// While an appeal waits, the form is not offered again; after a rejection the seller's answer is shown (escaped) with the form, so the customer can answer it.
func TestTheAppealPageShowsAWaitingAppealAndTheSellersAnswerEscaped(t *testing.T) {
	ts, plans := newTestServer(t)
	plans.customer = &plansv1.Customer{Id: "cust-uuid", ExternalId: "ada-ext", Locale: "en", BlockTime: timestamppb.Now()}
	plans.appeals = []*plansv1.CustomerBlockAppeal{{Id: "ap-1", Status: "open", Statement: "please"}}
	if _, page := get(t, ts, "/c/"+portalTok+"/appeal"); !strings.Contains(page, "has not answered yet") || strings.Contains(page, `name="statement"`) || strings.Contains(page, "please") {
		t.Errorf("a waiting appeal:\n%s", page)
	}
	plans.appeals = []*plansv1.CustomerBlockAppeal{{Id: "ap-1", Status: "rejected", Statement: "please", Answer: "No <i>way</i>"}}
	_, page := get(t, ts, "/c/"+portalTok+"/appeal")
	if !strings.Contains(page, "No &lt;i&gt;way&lt;/i&gt;") || strings.Contains(page, "<i>way") || !strings.Contains(page, `name="statement"`) {
		t.Errorf("a rejected appeal:\n%s", page)
	}
}

// Posting is rate limited like the other public forms that write: the eleventh post of an address in a minute is a 429, and nothing reaches plans-svc.
func TestAppealPostsAreRateLimited(t *testing.T) {
	ts, plans := newTestServer(t)
	plans.customer = &plansv1.Customer{Id: "cust-uuid", ExternalId: "ada-ext", BlockTime: timestamppb.Now()}
	var last int
	for range hostedPaysPerMinute + 1 {
		r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/appeal", url.Values{"statement": {"x"}}, nil)
		last = r.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("the post over the limit: %d", last)
	}
}

// The seller's side: the tenant API lists the appeals and decides one. Who decides is the gateway's word, and a read-only key cannot decide.
func TestTheSellerDecidesAnAppealAsTheKeyAndNotAsTheBodySays(t *testing.T) {
	ts, f := newTestServer(t)
	f.appeals = []*plansv1.CustomerBlockAppeal{{Id: "ap-1", Status: "open", Statement: "please"}}
	if r, body := do(t, ts, http.MethodGet, "/v1/customers/c1/appeals", "keyed-key", ""); r.StatusCode != http.StatusOK || body["appeals"] == nil {
		t.Errorf("list: %d %v", r.StatusCode, body)
	}
	r, body := do(t, ts, http.MethodPost, "/v1/customers/c1/appeals/ap-1/decision", "keyed-key", `{"decision":"rejected","answer":"No","actor":"forged@example.com","appeal_id":"other","external_id":"other"}`)
	got := f.lastDecide
	if r.StatusCode != http.StatusOK || got.GetActor() != "key:key-123" || got.GetExternalId() != "c1" || got.GetAppealId() != "ap-1" || got.GetDecision() != "rejected" || got.GetAnswer() != "No" || body["status"] != "rejected" {
		t.Errorf("decide: %d %v %v", r.StatusCode, body, got)
	}
}
