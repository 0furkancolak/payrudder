//go:build integration

package itest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// An operator can ban an account (every live tenant of it behaves as banned, its sandbox and its rows are untouched, lifting restores it) and a person (no sign-in, no
// session). Both need a reason and are kept in an append-only history.
func testBans(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	t.Run("an account ban makes its live tenants banned without rewriting them", func(t *testing.T) {
		tenants := e.account(t, "Banned Org")
		live, sandbox := tenants[plansv1.Environment_ENVIRONMENT_LIVE], tenants[plansv1.Environment_ENVIRONMENT_SANDBOX]
		accountID := live.GetAccountId()
		key, err := e.admin.CreateApiKey(operator(), &plansv1.CreateApiKeyRequest{TenantId: live.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		ban := func(banned bool, reason string) error {
			_, err := e.admin.SetBan(operator(), &plansv1.SetBanRequest{AccountId: accountID, Banned: banned, Category: "fraud", Reason: reason, Actor: "alice@example.com"})
			return err
		}
		resolved := func() plansv1.TenantStatus {
			t.Helper()
			r, err := e.admin.ResolveApiKey(as(live.GetId()), &plansv1.ResolveApiKeyRequest{Prefix: key.GetApiKey().GetPrefix()})
			if err != nil {
				t.Fatal(err)
			}
			return r.GetTenantStatus()
		}
		statusOf := func(id string) plansv1.TenantStatus {
			t.Helper()
			r, err := e.admin.GetTenant(operator(), &plansv1.GetTenantRequest{TenantId: id})
			if err != nil {
				t.Fatal(err)
			}
			return r.GetTenant().GetStatus()
		}
		wantCode(t, "a ban without a reason", ban(true, " "), codes.InvalidArgument, "plans.tenant.reason_required")
		if err := ban(true, "card testing"); err != nil {
			t.Fatal(err)
		}
		if resolved() != sBanned || statusOf(live.GetId()) != sBanned || statusOf(sandbox.GetId()) != sActive {
			t.Fatalf("after the ban: key %v, live %v, sandbox %v", resolved(), statusOf(live.GetId()), statusOf(sandbox.GetId()))
		}
		if err := ban(false, "mistake"); err != nil {
			t.Fatal(err)
		}
		if resolved() != sActive || statusOf(live.GetId()) != sActive {
			t.Fatalf("after the lift: key %v, live %v", resolved(), statusOf(live.GetId()))
		}
		h, err := e.admin.ListBanEvents(operator(), &plansv1.ListBanEventsRequest{AccountId: accountID})
		if err != nil || h.GetBanned() || len(h.GetEvents()) != 2 || h.GetEvents()[0].GetKind() != "lifted" || h.GetEvents()[1].GetKind() != "banned" ||
			h.GetEvents()[1].GetReason() != "card testing" || h.GetEvents()[1].GetActor() != "alice@example.com" {
			t.Fatalf("history = %v, %v", h, err)
		}
		if err := ban(false, "again"); err != nil { // lifting what is not banned changes and records nothing
			t.Fatal(err)
		}
		if h, _ = e.admin.ListBanEvents(operator(), &plansv1.ListBanEventsRequest{AccountId: accountID}); len(h.GetEvents()) != 2 {
			t.Fatalf("a lift of nothing was recorded: %v", h)
		}
	})

	t.Run("a person ban ends their sessions and refuses sign-in, until lifted", func(t *testing.T) {
		in := e.signUp(t, "mallory@example.com", "Mallory Co")
		ban := func(banned bool) error {
			_, err := e.admin.SetBan(operator(), &plansv1.SetBanRequest{Email: "Mallory@Example.com", Banned: banned, Reason: "abuse", Actor: "alice@example.com"})
			return err
		}
		if err := ban(true); err != nil {
			t.Fatal(err)
		}
		_, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: in.GetToken()})
		wantCode(t, "the session of a banned person", err, codes.Unauthenticated, "")
		_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "mallory@example.com", Password: goodPassword})
		wantCode(t, "sign-in of a banned person", err, codes.PermissionDenied, "identity.auth.user_banned")
		_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "mallory@example.com", Password: "not the password"})
		wantCode(t, "a wrong password says nothing about the ban", err, codes.Unauthenticated, "identity.auth.invalid_credentials")
		if err := ban(false); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "mallory@example.com", Password: goodPassword}); err != nil {
			t.Fatalf("sign-in after the lift: %v", err)
		}
		if _, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: in.GetToken()}); err == nil {
			t.Fatal("the session that ended with the ban came back")
		}
		_, err = e.admin.SetBan(operator(), &plansv1.SetBanRequest{Email: "nobody@example.com", Banned: true, Reason: "x"})
		wantCode(t, "an unknown person", err, codes.NotFound, "")
		_, err = e.admin.SetBan(operator(), &plansv1.SetBanRequest{Banned: true, Reason: "x"})
		wantCode(t, "no subject", err, codes.InvalidArgument, "")
	})
}

// With the ban approval on, banning an account waits for a second operator; lifting does not.
func testBansApproval(t *testing.T) {
	banApproval = true
	t.Cleanup(func() { banApproval = false })
	e := newEnv(t)
	accountID := e.account(t, "Approved Org")[plansv1.Environment_ENVIRONMENT_LIVE].GetAccountId()
	ban := func(actor string) error {
		_, err := e.admin.SetBan(operator(), &plansv1.SetBanRequest{AccountId: accountID, Banned: true, Reason: "fraud ring", Actor: actor})
		return err
	}
	wantCode(t, "the first request", ban("alice@example.com"), codes.FailedPrecondition, "plans.tenant.approval_pending")
	wantCode(t, "the same operator again", ban("alice@example.com"), codes.FailedPrecondition, "plans.tenant.approval_pending")
	if err := ban("bob@example.com"); err != nil {
		t.Fatalf("the second operator: %v", err)
	}
	h, err := e.admin.ListBanEvents(operator(), &plansv1.ListBanEventsRequest{AccountId: accountID})
	if err != nil || !h.GetBanned() || len(h.GetEvents()) != 3 || h.GetEvents()[0].GetKind() != "banned" || h.GetEvents()[0].GetApprovedBy() != "alice@example.com" {
		t.Fatalf("history = %v, %v", h, err)
	}
	if _, err := e.admin.SetBan(operator(), &plansv1.SetBanRequest{AccountId: accountID, Reason: "cleared", Actor: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}
}
