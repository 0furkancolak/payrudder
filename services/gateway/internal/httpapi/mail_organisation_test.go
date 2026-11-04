package httpapi

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// fakeOrganisationMail records what the gateway sent: the person and the organisation come from the session, never from a body.
type fakeOrganisationMail struct {
	mu                      sync.Mutex
	put                     *plansv1.PutOrganisationMailAccountRequest
	bound                   *plansv1.BindOrganisationMailAccountRequest
	unbound                 *plansv1.UnbindOrganisationMailAccountRequest
	deleted                 *plansv1.DeleteOrganisationMailAccountRequest
	listed                  *plansv1.ListOrganisationMailAccountsRequest
	deleteAnswer, bindError error
}

func (f *fakeOrganisationMail) ListOrganisationMailAccounts(_ context.Context, in *plansv1.ListOrganisationMailAccountsRequest, _ ...grpc.CallOption) (*plansv1.ListOrganisationMailAccountsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = in
	return &plansv1.ListOrganisationMailAccountsResponse{CanWrite: true, Accounts: []*plansv1.OrganisationMailAccount{{Account: "nafru~eu", UsedBy: []*plansv1.OrganisationMailApp{{AppId: adaApp, Name: "Shop"}}}}}, nil
}

func (f *fakeOrganisationMail) PutOrganisationMailAccount(_ context.Context, in *plansv1.PutOrganisationMailAccountRequest, _ ...grpc.CallOption) (*plansv1.PutOrganisationMailAccountResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.put = in
	return &plansv1.PutOrganisationMailAccountResponse{Account: &plansv1.OrganisationMailAccount{Account: "nafru~" + in.GetLabel(), KeyHint: "1234"}}, nil
}

func (f *fakeOrganisationMail) DeleteOrganisationMailAccount(_ context.Context, in *plansv1.DeleteOrganisationMailAccountRequest, _ ...grpc.CallOption) (*plansv1.DeleteOrganisationMailAccountResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = in
	return &plansv1.DeleteOrganisationMailAccountResponse{}, f.deleteAnswer
}

func (f *fakeOrganisationMail) TestOrganisationMailAccount(context.Context, *plansv1.TestOrganisationMailAccountRequest, ...grpc.CallOption) (*plansv1.TestOrganisationMailAccountResponse, error) {
	return &plansv1.TestOrganisationMailAccountResponse{Ok: true}, nil
}

func (f *fakeOrganisationMail) BindOrganisationMailAccount(_ context.Context, in *plansv1.BindOrganisationMailAccountRequest, _ ...grpc.CallOption) (*plansv1.BindOrganisationMailAccountResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bound = in
	return &plansv1.BindOrganisationMailAccountResponse{}, f.bindError
}

func (f *fakeOrganisationMail) UnbindOrganisationMailAccount(_ context.Context, in *plansv1.UnbindOrganisationMailAccountRequest, _ ...grpc.CallOption) (*plansv1.UnbindOrganisationMailAccountResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unbound = in
	return &plansv1.UnbindOrganisationMailAccountResponse{}, nil
}

// The organisation's e-mail accounts: the person and the organisation are the session's (a body that names others is overwritten), a delete names its account in the
// query, plans-svc's refusal reaches the caller with its key, and another organisation's id in the query is not found.
func TestOrganisationMailRoutes(t *testing.T) {
	om := &fakeOrganisationMail{}
	p := newProduct(t, nil, func(d *Deps) { d.OrganisationMail = om })
	base := "/app/v1/account/integrations/mail"

	if resp, m := p.call(t, "GET", base, tokViewer, "", ""); resp.StatusCode != 200 || om.listed.GetUserId() != viewerUser || om.listed.GetAccountId() != adaAcct || m["can_write"] != true {
		t.Fatalf("list: %d %v %v", resp.StatusCode, m, om.listed)
	}
	body := `{"user_id":"` + bobUser + `","account_id":"` + bobAcct + `","provider":"nafru","label":"eu","base_url":"https://api.nafru.example","api_key":"nk_1234","from":"Acme <a@acme.example>"}`
	resp, m := p.call(t, "PUT", base, tokAda, "", body)
	if resp.StatusCode != 200 || om.put.GetUserId() != adaUser || om.put.GetAccountId() != adaAcct || om.put.GetLabel() != "eu" || om.put.GetApiKey() != "nk_1234" {
		t.Fatalf("put: %d %v %v", resp.StatusCode, m, om.put)
	}
	if _, has := m["api_key"]; has {
		t.Errorf("the key is in the answer: %v", m)
	}
	if resp, _ := p.call(t, "POST", base+"/bind", tokAda, "", `{"account":"nafru~eu","app_id":"`+adaApp+`","make_default":true}`); resp.StatusCode != 200 || om.bound.GetAppId() != adaApp || !om.bound.GetMakeDefault() || om.bound.GetUserId() != adaUser {
		t.Errorf("bind: %d %v", resp.StatusCode, om.bound)
	}
	if resp, _ := p.call(t, "POST", base+"/unbind", tokAda, "", `{"account":"nafru~eu","app_id":"`+adaApp+`"}`); resp.StatusCode != 200 || om.unbound.GetAccount() != "nafru~eu" {
		t.Errorf("unbind: %d %v", resp.StatusCode, om.unbound)
	}
	om.deleteAnswer = errkey.New(codes.FailedPrecondition, "plans.mail_integration.shared_in_use", "apps use this account: Shop")
	resp, m = p.call(t, "DELETE", base+"?account=nafru~eu", tokAda, "", "")
	if resp.StatusCode != 409 || errKeyOf(m) != "plans.mail_integration.shared_in_use" || om.deleted.GetAccount() != "nafru~eu" || om.deleted.GetUserId() != adaUser {
		t.Errorf("delete of a used account: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "GET", base+"?account_id="+bobAcct, tokAda, "", ""); resp.StatusCode != 404 || errKeyOf(m) != "identity.account.not_found" {
		t.Errorf("another organisation: %d %v", resp.StatusCode, m)
	}
}
