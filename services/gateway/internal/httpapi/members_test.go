package httpapi

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
)

// The members and invitations routes against a fake identity service that enforces the same role rules as the real one (the real ones are tested in
// plans-svc against Postgres): what matters here is that the gateway takes the user from the session, the account from the session's memberships, and
// nothing about either from a body.

const goodInviteToken = "qi_good"

// memberCalls records what the members routes sent to plans-svc.
type memberCalls struct {
	invites []*plansv1.InviteMemberRequest
	roles   []*plansv1.UpdateMemberRoleRequest
	removes []*plansv1.RemoveMemberRequest
	revokes []*plansv1.RevokeInvitationRequest
	renames []*plansv1.RenameMyAccountRequest
	accepts []*plansv1.AcceptInvitationRequest
	asUser  []*plansv1.AcceptInvitationAsUserRequest
	lookups []*plansv1.LookupInvitationRequest
}

func (f *fakeIdentity) RenameMyAccount(_ context.Context, req *plansv1.RenameMyAccountRequest) (*plansv1.RenameMyAccountResponse, error) {
	f.seen(req.GetUserId())
	f.mu.Lock()
	f.mem.renames = append(f.mem.renames, req)
	f.mu.Unlock()
	switch f.role(req.GetUserId(), req.GetAccountId()) {
	case "":
		return nil, notFound("account")
	case "owner":
		return &plansv1.RenameMyAccountResponse{Name: req.GetName()}, nil
	}
	return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "owners only")
}

func (f *fakeIdentity) InviteMember(_ context.Context, req *plansv1.InviteMemberRequest) (*plansv1.InviteMemberResponse, error) {
	f.seen(req.GetUserId())
	f.mu.Lock()
	f.mem.invites = append(f.mem.invites, req)
	f.mu.Unlock()
	switch f.role(req.GetUserId(), req.GetAccountId()) {
	case "":
		return nil, notFound("account")
	case "viewer":
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "read-only")
	}
	return &plansv1.InviteMemberResponse{Invitation: &plansv1.Invitation{Id: "inv1", Email: req.GetEmail(), Role: req.GetRole(), ExpireTime: timestamppb.Now()}}, nil
}

func (f *fakeIdentity) ListInvitations(_ context.Context, req *plansv1.ListInvitationsRequest) (*plansv1.ListInvitationsResponse, error) {
	f.seen(req.GetUserId())
	switch f.role(req.GetUserId(), req.GetAccountId()) {
	case "":
		return nil, notFound("account")
	case "viewer":
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "owners and administrators only")
	}
	return &plansv1.ListInvitationsResponse{Invitations: []*plansv1.Invitation{{Id: "inv1", Email: "new@example.com", Role: "viewer"}}}, nil
}

func (f *fakeIdentity) RevokeInvitation(_ context.Context, req *plansv1.RevokeInvitationRequest) (*plansv1.RevokeInvitationResponse, error) {
	f.seen(req.GetUserId())
	f.mu.Lock()
	f.mem.revokes = append(f.mem.revokes, req)
	f.mu.Unlock()
	switch f.role(req.GetUserId(), req.GetAccountId()) {
	case "":
		return nil, notFound("account")
	case "viewer":
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "read-only")
	}
	return &plansv1.RevokeInvitationResponse{}, nil
}

func (f *fakeIdentity) ResendInvitation(_ context.Context, req *plansv1.ResendInvitationRequest) (*plansv1.ResendInvitationResponse, error) {
	f.seen(req.GetUserId())
	if f.role(req.GetUserId(), req.GetAccountId()) == "" {
		return nil, notFound("account")
	}
	return &plansv1.ResendInvitationResponse{Invitation: &plansv1.Invitation{Id: req.GetInvitationId()}}, nil
}

func (f *fakeIdentity) UpdateMemberRole(_ context.Context, req *plansv1.UpdateMemberRoleRequest) (*plansv1.UpdateMemberRoleResponse, error) {
	f.seen(req.GetUserId())
	f.mu.Lock()
	f.mem.roles = append(f.mem.roles, req)
	f.mu.Unlock()
	switch f.role(req.GetUserId(), req.GetAccountId()) {
	case "":
		return nil, notFound("account")
	case "viewer":
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "read-only")
	}
	if req.GetMemberUserId() == adaUser && req.GetRole() != "owner" {
		return nil, errkey.New(codes.FailedPrecondition, "identity.member.last_owner", "keep an owner")
	}
	return &plansv1.UpdateMemberRoleResponse{Role: req.GetRole()}, nil
}

func (f *fakeIdentity) RemoveMember(_ context.Context, req *plansv1.RemoveMemberRequest) (*plansv1.RemoveMemberResponse, error) {
	f.seen(req.GetUserId())
	f.mu.Lock()
	f.mem.removes = append(f.mem.removes, req)
	f.mu.Unlock()
	role := f.role(req.GetUserId(), req.GetAccountId())
	if role == "" {
		return nil, notFound("account")
	}
	if role == "viewer" && req.GetMemberUserId() != req.GetUserId() {
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "read-only")
	}
	return &plansv1.RemoveMemberResponse{}, nil
}

func (f *fakeIdentity) LookupInvitation(_ context.Context, req *plansv1.LookupInvitationRequest) (*plansv1.LookupInvitationResponse, error) {
	f.mu.Lock()
	f.mem.lookups = append(f.mem.lookups, req)
	f.mu.Unlock()
	if req.GetToken() != goodInviteToken {
		return nil, errkey.New(codes.InvalidArgument, "identity.token.invalid", "bad link")
	}
	return &plansv1.LookupInvitationResponse{AccountName: "Ada Labs", Email: "new@example.com", Role: "viewer", HasAccount: false}, nil
}

func (f *fakeIdentity) AcceptInvitation(_ context.Context, req *plansv1.AcceptInvitationRequest) (*plansv1.AcceptInvitationResponse, error) {
	f.mu.Lock()
	f.mem.accepts = append(f.mem.accepts, req)
	f.mu.Unlock()
	switch req.GetToken() {
	case goodInviteToken:
		return &plansv1.AcceptInvitationResponse{User: &plansv1.User{Id: adaUser, Email: "new@example.com", EmailVerified: true}, Token: tokAda,
			Memberships: f.memberships(adaUser), ExpireTime: timestamppb.Now()}, nil
	case "qi_existing":
		return nil, errkey.New(codes.FailedPrecondition, "identity.invite.sign_in_required", "sign in")
	}
	return nil, errkey.New(codes.InvalidArgument, "identity.token.invalid", "bad link")
}

func (f *fakeIdentity) AcceptInvitationAsUser(_ context.Context, req *plansv1.AcceptInvitationAsUserRequest) (*plansv1.AcceptInvitationAsUserResponse, error) {
	f.seen(req.GetUserId())
	f.mu.Lock()
	f.mem.asUser = append(f.mem.asUser, req)
	f.mu.Unlock()
	if req.GetToken() != goodInviteToken {
		return nil, errkey.New(codes.InvalidArgument, "identity.token.invalid", "bad link")
	}
	if req.GetUserId() != adaUser {
		return nil, errkey.New(codes.PermissionDenied, "identity.invite.wrong_user", "another address")
	}
	return &plansv1.AcceptInvitationAsUserResponse{Memberships: f.memberships(adaUser), AccountId: adaAcct}, nil
}

func TestMemberRoutesNeedALiveSession(t *testing.T) {
	p := newProduct(t, nil, nil)
	for _, rt := range []struct{ method, path, body string }{
		{"PATCH", "/app/v1/account", `{"name":"x"}`},
		{"GET", "/app/v1/account/invitations", ""},
		{"POST", "/app/v1/account/invitations", `{"email":"a@example.com","role":"viewer"}`},
		{"DELETE", "/app/v1/account/invitations/inv1", ""},
		{"POST", "/app/v1/account/invitations/inv1/resend", ""},
		{"PATCH", "/app/v1/account/members/" + bobUser, `{"role":"viewer"}`},
		{"DELETE", "/app/v1/account/members/" + bobUser, ""},
		{"POST", "/app/v1/invitations/accept", `{"token":"qi_good"}`},
	} {
		for _, tok := range []string{"", "qs_unknown", "live-key", operatorToken} {
			if resp, _ := p.call(t, rt.method, rt.path, tok, "", rt.body); resp.StatusCode != 401 {
				t.Errorf("%s %s with token %q: %d, want 401", rt.method, rt.path, tok, resp.StatusCode)
			}
		}
	}
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if n := len(p.ident.mem.invites) + len(p.ident.mem.roles) + len(p.ident.mem.removes) + len(p.ident.mem.revokes) + len(p.ident.mem.renames) + len(p.ident.mem.asUser); n != 0 {
		t.Errorf("%d calls reached plans-svc without a session", n)
	}
}

func TestMemberRoutesActAsTheSessionsUserInTheSessionsAccount(t *testing.T) {
	p := newProduct(t, nil, nil)
	// A body that names another user and account changes nothing: the session decides both.
	resp, m := p.call(t, "POST", "/app/v1/account/invitations", tokAda, "",
		`{"email":"new@example.com","role":"viewer","user_id":"`+bobUser+`","account_id":"`+bobAcct+`"}`)
	if resp.StatusCode != 201 || m["email"] != "new@example.com" || m["token"] != nil {
		t.Fatalf("invite: %d %v", resp.StatusCode, m)
	}
	resp, _ = p.call(t, "PATCH", "/app/v1/account/members/"+bobUser, tokAda, "", `{"role":"admin","user_id":"`+bobUser+`"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("role: %d", resp.StatusCode)
	}
	if resp, _ = p.call(t, "DELETE", "/app/v1/account/members/"+viewerUser, tokAda, "", ""); resp.StatusCode != 204 {
		t.Fatalf("remove: %d", resp.StatusCode)
	}
	if resp, _ = p.call(t, "DELETE", "/app/v1/account/invitations/inv1", tokAda, "", ""); resp.StatusCode != 204 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp, m = p.call(t, "PATCH", "/app/v1/account", tokAda, "", `{"name":"Ada Holdings","account_id":"`+bobAcct+`"}`); resp.StatusCode != 200 || m["name"] != "Ada Holdings" {
		t.Fatalf("rename: %d %v", resp.StatusCode, m)
	}
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if r := p.ident.mem.invites[0]; r.GetUserId() != adaUser || r.GetAccountId() != adaAcct {
		t.Errorf("invite carried %s/%s", r.GetUserId(), r.GetAccountId())
	}
	if r := p.ident.mem.roles[0]; r.GetUserId() != adaUser || r.GetAccountId() != adaAcct || r.GetMemberUserId() != bobUser || r.GetRole() != "admin" {
		t.Errorf("role change carried %v", r)
	}
	if r := p.ident.mem.removes[0]; r.GetUserId() != adaUser || r.GetAccountId() != adaAcct || r.GetMemberUserId() != viewerUser {
		t.Errorf("removal carried %v", r)
	}
	if r := p.ident.mem.renames[0]; r.GetUserId() != adaUser || r.GetAccountId() != adaAcct {
		t.Errorf("rename carried %v", r)
	}
}

func TestMemberRoutesRefuseAnotherAccountBeforeAskingPlans(t *testing.T) {
	p := newProduct(t, nil, nil)
	// Bob names Ada's account: it is not one of his session's accounts, so the gateway answers 404 and plans-svc is not asked.
	for _, rt := range []struct{ method, path, body string }{
		{"GET", "/app/v1/account/invitations?account_id=" + adaAcct, ""},
		{"POST", "/app/v1/account/invitations?account_id=" + adaAcct, `{"email":"a@example.com","role":"viewer"}`},
		{"DELETE", "/app/v1/account/invitations/inv1?account_id=" + adaAcct, ""},
		{"POST", "/app/v1/account/invitations/inv1/resend?account_id=" + adaAcct, ""},
		{"PATCH", "/app/v1/account/members/" + adaUser + "?account_id=" + adaAcct, `{"role":"viewer"}`},
		{"DELETE", "/app/v1/account/members/" + adaUser + "?account_id=" + adaAcct, ""},
		{"PATCH", "/app/v1/account?account_id=" + adaAcct, `{"name":"mine now"}`},
	} {
		resp, m := p.call(t, rt.method, rt.path, tokBob, "", rt.body)
		if resp.StatusCode != 404 || errKeyOf(m) != "identity.account.not_found" {
			t.Errorf("%s %s: %d %v, want 404 identity.account.not_found", rt.method, rt.path, resp.StatusCode, m)
		}
	}
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if p.ident.lastUser != "" {
		t.Errorf("plans-svc was asked on behalf of %s", p.ident.lastUser)
	}
}

func TestViewersReadAndLeaveButManageNothing(t *testing.T) {
	p := newProduct(t, nil, nil)
	for _, rt := range []struct{ method, path, body string }{
		{"GET", "/app/v1/account/invitations", ""},
		{"POST", "/app/v1/account/invitations", `{"email":"a@example.com","role":"viewer"}`},
		{"DELETE", "/app/v1/account/invitations/inv1", ""},
		{"PATCH", "/app/v1/account/members/" + bobUser, `{"role":"viewer"}`},
		{"DELETE", "/app/v1/account/members/" + adaUser, ""},
		{"PATCH", "/app/v1/account", `{"name":"x"}`},
	} {
		resp, m := p.call(t, rt.method, rt.path, tokViewer, "", rt.body)
		if resp.StatusCode != 403 || errKeyOf(m) != "identity.account.forbidden" {
			t.Errorf("%s %s: %d %v, want 403 identity.account.forbidden", rt.method, rt.path, resp.StatusCode, m)
		}
	}
	if resp, _ := p.call(t, "DELETE", "/app/v1/account/members/"+viewerUser, tokViewer, "", ""); resp.StatusCode != 204 {
		t.Errorf("a viewer leaves: %d", resp.StatusCode)
	}
	resp, m := p.call(t, "PATCH", "/app/v1/account/members/"+adaUser, tokAda, "", `{"role":"viewer"}`)
	if resp.StatusCode != 409 || errKeyOf(m) != "identity.member.last_owner" {
		t.Errorf("last owner: %d %v", resp.StatusCode, m)
	}
}

func TestInvitationAcceptRoutes(t *testing.T) {
	p := newProduct(t, nil, nil)
	// Public: no credential needed, the token is the credential. It is read from a body, never from the URL.
	resp, m := p.call(t, "POST", "/auth/invitations/lookup", "", "", `{"token":"`+goodInviteToken+`"}`)
	if resp.StatusCode != 200 || m["account_name"] != "Ada Labs" || m["has_account"] != false || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("lookup: %d %v", resp.StatusCode, m)
	}
	if resp, m = p.call(t, "POST", "/auth/invitations/lookup", "", "", `{"token":"qi_old"}`); resp.StatusCode != 400 || errKeyOf(m) != "identity.token.invalid" {
		t.Fatalf("bad lookup: %d %v", resp.StatusCode, m)
	}
	resp, m = p.call(t, "POST", "/auth/invitations/accept", "", "", `{"token":"`+goodInviteToken+`","password":"a long enough password","display_name":"Ned","locale":"tr"}`)
	if resp.StatusCode != 200 || m["token"] != tokAda || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("accept: %d %v", resp.StatusCode, m)
	}
	if resp, m = p.call(t, "POST", "/auth/invitations/accept", "", "", `{"token":"qi_existing","password":"a long enough password"}`); resp.StatusCode != 409 || errKeyOf(m) != "identity.invite.sign_in_required" {
		t.Fatalf("existing address: %d %v", resp.StatusCode, m)
	}
	// The signed-in way: the user comes from the session, a body cannot name another.
	resp, m = p.call(t, "POST", "/app/v1/invitations/accept", tokAda, "", `{"token":"`+goodInviteToken+`","user_id":"`+bobUser+`"}`)
	if resp.StatusCode != 200 || m["account_id"] != adaAcct {
		t.Fatalf("accept as user: %d %v", resp.StatusCode, m)
	}
	resp, m = p.call(t, "POST", "/app/v1/invitations/accept", tokBob, "", `{"token":"`+goodInviteToken+`"}`)
	if resp.StatusCode != 403 || errKeyOf(m) != "identity.invite.wrong_user" {
		t.Fatalf("accept for another address: %d %v", resp.StatusCode, m)
	}
	p.ident.mu.Lock()
	defer p.ident.mu.Unlock()
	if p.ident.mem.asUser[0].GetUserId() != adaUser {
		t.Errorf("accept carried user %s", p.ident.mem.asUser[0].GetUserId())
	}
	if a := p.ident.mem.accepts[0]; a.GetLocale() != "tr" || a.GetDisplayName() != "Ned" {
		t.Errorf("accept carried %v", a)
	}
}

func TestInvitationLinksAreRateLimitedPerAddress(t *testing.T) {
	p := newProduct(t, nil, nil)
	for i := 0; i < tokenUsesPerMinute+2; i++ {
		resp, m := p.call(t, "POST", "/auth/invitations/lookup", "", "", `{"token":"qi_guess"}`)
		if i >= tokenUsesPerMinute && (resp.StatusCode != 429 || errKeyOf(m) != "identity.auth.rate_limited") {
			t.Fatalf("request %d: %d %v", i+1, resp.StatusCode, m)
		}
	}
}
