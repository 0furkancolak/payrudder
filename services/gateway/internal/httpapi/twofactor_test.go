package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

const (
	twoFactorEmail = "twofa@example.com"
	goodChallenge  = "qc_" + "0123456789012345678901234567890123456789012"
)

func (f *fakeIdentity) VerifyTwoFactor(_ context.Context, req *plansv1.VerifyTwoFactorRequest) (*plansv1.VerifyTwoFactorResponse, error) {
	f.mu.Lock()
	f.verifies = append(f.verifies, req)
	f.mu.Unlock()
	if req.GetChallenge() != goodChallenge || req.GetCode() != "123456" {
		return nil, errkey.New(codes.Unauthenticated, "identity.two_factor.invalid", "wrong")
	}
	return &plansv1.VerifyTwoFactorResponse{User: &plansv1.User{Id: adaUser, Email: twoFactorEmail}, Token: tokAda, Memberships: f.memberships(adaUser), ExpireTime: timestamppb.Now()}, nil
}

func (f *fakeIdentity) GetTwoFactor(_ context.Context, req *plansv1.GetTwoFactorRequest) (*plansv1.GetTwoFactorResponse, error) {
	f.seen(req.GetUserId())
	return &plansv1.GetTwoFactorResponse{Enabled: true, RecoveryCodesRemaining: 7}, nil
}

func (f *fakeIdentity) DisableTwoFactor(_ context.Context, req *plansv1.DisableTwoFactorRequest) (*plansv1.DisableTwoFactorResponse, error) {
	f.seen(req.GetUserId())
	if req.GetCode() != "123456" {
		return nil, errkey.New(codes.InvalidArgument, "identity.two_factor.bad_code", "wrong")
	}
	return &plansv1.DisableTwoFactorResponse{}, nil
}

func (f *fakeIdentity) SetAccountSecurity(_ context.Context, req *plansv1.SetAccountSecurityRequest) (*plansv1.SetAccountSecurityResponse, error) {
	f.seen(req.GetUserId())
	return &plansv1.SetAccountSecurityResponse{RequireTwoFactorForOwners: req.GetRequireTwoFactorForOwners()}, nil
}

func TestSignInWithTwoFactorGivesAChallengeAndNoSession(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "POST", "/auth/signin", "", "", `{"email":"`+twoFactorEmail+`","password":"right password!"}`)
	if resp.StatusCode != 200 || m["second_factor_required"] != true || m["challenge"] != goodChallenge || m["challenge_expire_time"] != "2026-11-02T12:00:00Z" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("first step: %d %v", resp.StatusCode, m)
	}
	for _, k := range []string{"token", "user", "memberships", "operator"} {
		if _, has := m[k]; has {
			t.Errorf("the answer must not carry %q before the second step: %v", k, m)
		}
	}
	// The second step: a session, or the same refusal for every fault.
	resp, m = p.call(t, "POST", "/auth/2fa/verify", "", "", `{"challenge":"`+goodChallenge+`","code":"123456"}`)
	if resp.StatusCode != 200 || m["token"] != tokAda || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("verify: %d %v", resp.StatusCode, m)
	}
	resp, m = p.call(t, "POST", "/auth/2fa/verify", "", "", `{"challenge":"`+goodChallenge+`","code":"000000"}`)
	if resp.StatusCode != 401 || errKeyOf(m) != "identity.two_factor.invalid" {
		t.Fatalf("wrong code: %d %v", resp.StatusCode, m)
	}
	// A person without it signs in as before.
	if resp, m := p.call(t, "POST", "/auth/signin", "", "", `{"email":"ada@example.com","password":"right password!"}`); resp.StatusCode != 200 || m["token"] != tokAda {
		t.Fatalf("plain sign-in: %d %v", resp.StatusCode, m)
	}
}

func TestSecondFactorRoutesAreRateLimitedPerAddress(t *testing.T) {
	p := newProduct(t, nil, nil)
	last := 0
	for range twoFactorPerMinute + 3 {
		resp, _ := p.call(t, "POST", "/auth/2fa/verify", "", "", `{"challenge":"`+goodChallenge+`","code":"000000"}`)
		last = resp.StatusCode
	}
	if last != 429 {
		t.Fatalf("after %d tries from one address the next is %d, want 429", twoFactorPerMinute+2, last)
	}
	// The routes that take a code share the limit.
	if resp, m := p.call(t, "POST", "/auth/2fa/disable", tokAda, "", `{"code":"123456"}`); resp.StatusCode != 429 || errKeyOf(m) != "identity.auth.rate_limited" {
		t.Errorf("disable from the same address: %d %v", resp.StatusCode, m)
	}
}

func TestTwoFactorManagementIsThePersonsOwn(t *testing.T) {
	p := newProduct(t, nil, nil)
	if resp, _ := p.call(t, "GET", "/auth/2fa", "", "", ""); resp.StatusCode != 401 {
		t.Errorf("no session: %d", resp.StatusCode)
	}
	resp, m := p.call(t, "GET", "/auth/2fa", tokAda, "", "")
	if resp.StatusCode != 200 || m["enabled"] != true || m["recovery_codes_remaining"] != float64(7) || p.ident.lastUser != adaUser {
		t.Fatalf("status: %d %v as %q", resp.StatusCode, m, p.ident.lastUser)
	}
	if resp, m := p.call(t, "POST", "/auth/2fa/disable", tokAda, "", `{"code":"000000"}`); resp.StatusCode != 400 || errKeyOf(m) != "identity.two_factor.bad_code" {
		t.Errorf("wrong code: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", "/auth/2fa/disable", tokAda, "", `{"code":"123456"}`); resp.StatusCode != 204 {
		t.Errorf("disable: %d", resp.StatusCode)
	}
	if resp, m := p.call(t, "PATCH", "/app/v1/account/security", tokAda, "", `{"require_two_factor_for_owners":true}`); resp.StatusCode != 200 || m["require_two_factor_for_owners"] != true {
		t.Errorf("account security: %d %v", resp.StatusCode, m)
	}
}

func TestAnOwnerWhoMustSetUpTwoFactorCanDoNothingElse(t *testing.T) {
	p := newProduct(t, nil, nil)
	// What they can do: see who they are, set it up, sign out.
	for _, c := range []struct{ method, path string }{{"GET", "/auth/me"}, {"GET", "/auth/2fa"}} {
		if resp, m := p.call(t, c.method, c.path, tokNeedsSetup, "", ""); resp.StatusCode >= 400 {
			t.Errorf("%s %s: %d %v", c.method, c.path, resp.StatusCode, m)
		}
	}
	// Nothing else: not the product API, not the tenant API acting as a tenant of the account.
	for _, c := range []struct{ method, path, tenant, body string }{
		{"GET", "/app/v1/apps", "", ""},
		{"GET", "/app/v1/account", "", ""},
		{"PATCH", "/app/v1/account/security", "", `{"require_two_factor_for_owners":false}`},
		{"GET", "/v1/customers", adaLive, ""},
		{"POST", "/v1/customers", adaLive, `{"external_id":"c","name":"n","email":"a@x.io"}`},
	} {
		resp, m := p.call(t, c.method, c.path, tokNeedsSetup, c.tenant, c.body)
		if resp.StatusCode != 403 || errKeyOf(m) != "identity.two_factor.setup_required" {
			t.Errorf("%s %s: %d %v, want 403 identity.two_factor.setup_required", c.method, c.path, resp.StatusCode, m)
		}
	}
	if resp, _ := p.call(t, "POST", "/auth/signout", tokNeedsSetup, "", ""); resp.StatusCode != 204 {
		t.Errorf("sign out: %d", resp.StatusCode)
	}
	// The same person with a session that is not held is not refused.
	if resp, _ := p.call(t, "GET", "/app/v1/apps", tokAda, "", ""); resp.StatusCode != 200 {
		t.Errorf("an owner who has set it up: %d", resp.StatusCode)
	}
}

// Google returns the browser to the console, so the challenge travels in the URL fragment like a session token does: no server or proxy sees a fragment.
func TestChallengeRedirectKeepsTheChallengeInTheFragment(t *testing.T) {
	s := &Server{d: Deps{AppURL: "https://app.example.com"}}
	rec := httptest.NewRecorder()
	s.challengeRedirect(rec, httptest.NewRequest("GET", "/auth/google/callback", nil), goodChallenge, time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC))
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusFound || !strings.HasPrefix(loc, "https://app.example.com/signin/complete#challenge="+goodChallenge+"&expires=") || strings.Contains(strings.SplitN(loc, "#", 2)[0], "qc_") {
		t.Fatalf("redirect = %d %s", rec.Code, loc)
	}
}
