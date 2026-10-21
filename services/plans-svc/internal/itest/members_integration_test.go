//go:build integration

package itest

import (
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/usermail"
)

type person struct {
	id, email, account string
}

// verified signs a person up, signs them in and follows the verification link.
func (e *env) verified(t *testing.T, email, org string) person {
	t.Helper()
	in := e.signUp(t, email, org)
	e.verify(t, email)
	return person{id: in.GetUser().GetId(), email: email, account: in.GetMemberships()[0].GetAccountId()}
}

func (e *env) invite(p person, acct, email, role string) (*plansv1.InviteMemberResponse, error) {
	return e.ident.InviteMember(context.Background(), &plansv1.InviteMemberRequest{UserId: p.id, AccountId: acct, Email: email, Role: role})
}

func (e *env) roleOf(t *testing.T, acct, user string) string {
	t.Helper()
	var role string
	err := e.system.QueryRow(context.Background(), `SELECT role FROM account_members WHERE account_id = $1::uuid AND user_id = $2::uuid`, acct, user).Scan(&role)
	if err != nil {
		return ""
	}
	return role
}

// ageInvite makes the mail of the open invitation older than the one-a-minute rule.
func (e *env) ageInvite(t *testing.T, acct, email string) {
	t.Helper()
	if _, err := e.system.Exec(context.Background(), `UPDATE account_invites SET sent_at = now() - interval '5 minutes' WHERE account_id = $1::uuid AND lower(email) = lower($2) AND accepted_at IS NULL AND revoked_at IS NULL`, acct, email); err != nil {
		t.Fatal(err)
	}
}

// testMembersAndInvitations covers phase 39's members: invitations by e-mail, accepting as a new or an existing user, roles, removal, the last owner and
// every account-scoped call from a stranger.
func testMembersAndInvitations(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	olga := e.verified(t, "olga@members.example", "Olga Co")
	otto := e.verified(t, "otto@members.example", "Otto Co") // another account
	acct := olga.account

	t.Run("who may invite", func(t *testing.T) {
		unverified := e.signUp(t, "unverified@members.example", "Unverified Co")
		_, err := e.invite(person{id: unverified.GetUser().GetId()}, unverified.GetMemberships()[0].GetAccountId(), "x@members.example", "viewer")
		wantCode(t, "an unverified inviter", err, codes.PermissionDenied, "identity.email.not_verified")
		_, err = e.invite(olga, acct, "not an address", "viewer")
		wantCode(t, "bad address", err, codes.InvalidArgument, "")
		_, err = e.invite(olga, acct, "x@members.example", "boss")
		wantCode(t, "bad role", err, codes.InvalidArgument, "")
		_, err = e.invite(otto, acct, "x@members.example", "viewer")
		wantCode(t, "a stranger invites into another account", err, codes.NotFound, "identity.account.not_found")
		_, err = e.invite(olga, acct, "olga@members.example", "viewer")
		wantCode(t, "an existing member", err, codes.AlreadyExists, "identity.invite.already_member")
		_, err = e.invite(olga, "nope", "x@members.example", "viewer")
		wantCode(t, "malformed account id", err, codes.NotFound, "identity.account.not_found")
	})

	// Adam joins as administrator, Vera as viewer, through the real flow.
	adam := e.joinNew(t, olga, "adam@members.example", "admin")
	vera := e.joinNew(t, olga, "vera@members.example", "viewer")

	t.Run("the invitation mail and what is stored", func(t *testing.T) {
		ms := e.mail.to(usermail.KindInvite, "adam@members.example")
		if len(ms) != 1 || !strings.HasPrefix(ms[0].Link, testAppURL+"/invite#token=qi_") || ms[0].Account != "Olga Co" || ms[0].Role != "admin" || ms[0].Inviter == "" {
			t.Fatalf("invitation mail = %+v", ms)
		}
		if _, err := e.invite(olga, acct, "hash@members.example", "viewer"); err != nil {
			t.Fatal(err)
		}
		tok := e.mail.lastToken(usermail.KindInvite, "hash@members.example")
		var stored []byte
		var ttlHours float64
		if err := e.system.QueryRow(ctx, `SELECT token_hash, extract(epoch FROM expires_at - created_at) / 3600 FROM account_invites WHERE account_id = $1::uuid AND email = 'hash@members.example'`, acct).Scan(&stored, &ttlHours); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(tok))
		if string(stored) != string(sum[:]) {
			t.Error("the stored value is not the SHA-256 of the mailed token")
		}
		if ttlHours < 167 || ttlHours > 169 {
			t.Errorf("an invitation lives 7 days, got %.1f hours", ttlHours)
		}
	})

	t.Run("listing is for owners and administrators", func(t *testing.T) {
		for _, p := range []person{olga, adam} {
			l, err := e.ident.ListInvitations(ctx, &plansv1.ListInvitationsRequest{UserId: p.id, AccountId: acct})
			if err != nil || len(l.GetInvitations()) != 1 || l.GetInvitations()[0].GetEmail() != "hash@members.example" {
				t.Fatalf("%s lists %v, %v", p.email, l, err)
			}
			if l.GetInvitations()[0].GetInvitedByEmail() != "olga@members.example" {
				t.Errorf("inviter = %v", l.GetInvitations()[0])
			}
		}
		_, err := e.ident.ListInvitations(ctx, &plansv1.ListInvitationsRequest{UserId: vera.id, AccountId: acct})
		wantCode(t, "viewer lists invitations", err, codes.PermissionDenied, "identity.account.forbidden")
		_, err = e.ident.ListInvitations(ctx, &plansv1.ListInvitationsRequest{UserId: otto.id, AccountId: acct})
		wantCode(t, "stranger lists invitations", err, codes.NotFound, "identity.account.not_found")
	})

	t.Run("a refreshed invitation replaces the link", func(t *testing.T) {
		first := e.mail.lastToken(usermail.KindInvite, "hash@members.example")
		_, err := e.invite(olga, acct, "HASH@members.example", "admin")
		wantCode(t, "twice within a minute", err, codes.ResourceExhausted, "identity.invite.throttled")
		e.ageInvite(t, acct, "hash@members.example")
		if _, err := e.invite(olga, acct, "HASH@members.example", "admin"); err != nil {
			t.Fatal(err)
		}
		second := e.mail.lastToken(usermail.KindInvite, "hash@members.example")
		if second == first {
			t.Fatal("a new token")
		}
		_, err = e.ident.LookupInvitation(ctx, &plansv1.LookupInvitationRequest{Token: first})
		wantCode(t, "the replaced link", err, codes.InvalidArgument, "identity.token.invalid")
		l, err := e.ident.LookupInvitation(ctx, &plansv1.LookupInvitationRequest{Token: second})
		if err != nil || l.GetRole() != "admin" || l.GetEmail() != "hash@members.example" || l.GetAccountName() != "Olga Co" || l.GetHasAccount() {
			t.Fatalf("lookup = %v, %v", l, err)
		}
		var open int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM account_invites WHERE account_id = $1::uuid AND email = 'hash@members.example' AND accepted_at IS NULL AND revoked_at IS NULL`, acct).Scan(&open); err != nil || open != 1 {
			t.Fatalf("one open invitation per address: %d, %v", open, err)
		}
	})

	t.Run("an administrator cannot hand out ownership", func(t *testing.T) {
		_, err := e.invite(adam, acct, "boss@members.example", "owner")
		wantCode(t, "admin invites an owner", err, codes.PermissionDenied, "identity.account.forbidden")
		_, err = e.invite(vera, acct, "x2@members.example", "viewer")
		wantCode(t, "viewer invites", err, codes.PermissionDenied, "identity.account.forbidden")
		if _, err := e.invite(olga, acct, "co-owner@members.example", "owner"); err != nil {
			t.Fatalf("an owner invites an owner: %v", err)
		}
		l, _ := e.ident.ListInvitations(ctx, &plansv1.ListInvitationsRequest{UserId: olga.id, AccountId: acct})
		var ownerInvite string
		for _, i := range l.GetInvitations() {
			if i.GetRole() == "owner" {
				ownerInvite = i.GetId()
			}
		}
		if ownerInvite == "" {
			t.Fatal("the owner invitation is listed")
		}
		_, err = e.ident.RevokeInvitation(ctx, &plansv1.RevokeInvitationRequest{UserId: adam.id, AccountId: acct, InvitationId: ownerInvite})
		wantCode(t, "admin revokes an owner invitation", err, codes.PermissionDenied, "identity.account.forbidden")
		_, err = e.ident.ResendInvitation(ctx, &plansv1.ResendInvitationRequest{UserId: adam.id, AccountId: acct, InvitationId: ownerInvite})
		wantCode(t, "admin resends an owner invitation", err, codes.PermissionDenied, "identity.account.forbidden")
		if _, err := e.ident.RevokeInvitation(ctx, &plansv1.RevokeInvitationRequest{UserId: olga.id, AccountId: acct, InvitationId: ownerInvite}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("revoke and resend", func(t *testing.T) {
		l, _ := e.ident.ListInvitations(ctx, &plansv1.ListInvitationsRequest{UserId: olga.id, AccountId: acct})
		id := l.GetInvitations()[0].GetId()
		_, err := e.ident.RevokeInvitation(ctx, &plansv1.RevokeInvitationRequest{UserId: vera.id, AccountId: acct, InvitationId: id})
		wantCode(t, "viewer revokes", err, codes.PermissionDenied, "identity.account.forbidden")
		_, err = e.ident.RevokeInvitation(ctx, &plansv1.RevokeInvitationRequest{UserId: otto.id, AccountId: acct, InvitationId: id})
		wantCode(t, "stranger revokes", err, codes.NotFound, "identity.account.not_found")
		_, err = e.ident.RevokeInvitation(ctx, &plansv1.RevokeInvitationRequest{UserId: otto.id, AccountId: otto.account, InvitationId: id})
		wantCode(t, "another account's invitation id through one's own account", err, codes.NotFound, "identity.invite.not_found")
		_, err = e.ident.ResendInvitation(ctx, &plansv1.ResendInvitationRequest{UserId: olga.id, AccountId: acct, InvitationId: id})
		wantCode(t, "resend within a minute", err, codes.ResourceExhausted, "identity.invite.throttled")
		e.ageInvite(t, acct, l.GetInvitations()[0].GetEmail())
		before := e.mail.lastToken(usermail.KindInvite, l.GetInvitations()[0].GetEmail())
		if _, err := e.ident.ResendInvitation(ctx, &plansv1.ResendInvitationRequest{UserId: olga.id, AccountId: acct, InvitationId: id}); err != nil {
			t.Fatal(err)
		}
		if after := e.mail.lastToken(usermail.KindInvite, l.GetInvitations()[0].GetEmail()); after == before || after == "" {
			t.Error("a resend mails a new link")
		}
		tok := e.mail.lastToken(usermail.KindInvite, l.GetInvitations()[0].GetEmail())
		if _, err := e.ident.RevokeInvitation(ctx, &plansv1.RevokeInvitationRequest{UserId: adam.id, AccountId: acct, InvitationId: id}); err != nil {
			t.Fatal(err)
		}
		_, err = e.ident.LookupInvitation(ctx, &plansv1.LookupInvitationRequest{Token: tok})
		wantCode(t, "a revoked invitation", err, codes.InvalidArgument, "identity.token.invalid")
		_, err = e.ident.RevokeInvitation(ctx, &plansv1.RevokeInvitationRequest{UserId: olga.id, AccountId: acct, InvitationId: id})
		wantCode(t, "revoking twice", err, codes.NotFound, "identity.invite.not_found")
		_, err = e.ident.ResendInvitation(ctx, &plansv1.ResendInvitationRequest{UserId: olga.id, AccountId: acct, InvitationId: "nope"})
		wantCode(t, "malformed invitation id", err, codes.NotFound, "identity.invite.not_found")
	})

	t.Run("an expired invitation is dead, and does not block a new one", func(t *testing.T) {
		if _, err := e.invite(olga, acct, "late@members.example", "viewer"); err != nil {
			t.Fatal(err)
		}
		old := e.mail.lastToken(usermail.KindInvite, "late@members.example")
		if _, err := e.system.Exec(ctx, `UPDATE account_invites SET expires_at = now() - interval '1 second' WHERE account_id = $1::uuid AND email = 'late@members.example'`, acct); err != nil {
			t.Fatal(err)
		}
		_, err := e.ident.AcceptInvitation(ctx, &plansv1.AcceptInvitationRequest{Token: old, Password: goodPassword})
		wantCode(t, "accepting an expired invitation", err, codes.InvalidArgument, "identity.token.invalid")
		if _, err := e.invite(olga, acct, "late@members.example", "viewer"); err != nil {
			t.Fatalf("a new invitation to an address whose old one expired: %v", err)
		}
		if _, err := e.ident.LookupInvitation(ctx, &plansv1.LookupInvitationRequest{Token: e.mail.lastToken(usermail.KindInvite, "late@members.example")}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("accepting as a new user", func(t *testing.T) {
		const addr = "ned@members.example"
		if _, err := e.invite(olga, acct, addr, "viewer"); err != nil {
			t.Fatal(err)
		}
		tok := e.mail.lastToken(usermail.KindInvite, addr)
		_, err := e.ident.AcceptInvitation(ctx, &plansv1.AcceptInvitationRequest{Token: tok, Password: "short"})
		wantCode(t, "short password", err, codes.InvalidArgument, "")
		for _, bad := range []string{"", "qi_" + strings.Repeat("Z", 43), "qv_" + strings.Repeat("Z", 43)} {
			_, err = e.ident.AcceptInvitation(ctx, &plansv1.AcceptInvitationRequest{Token: bad, Password: goodPassword})
			wantCode(t, "bad token "+bad, err, codes.InvalidArgument, "identity.token.invalid")
		}
		// Two requests with one token: one new user.
		var wg sync.WaitGroup
		var mu sync.Mutex
		var wins []*plansv1.AcceptInvitationResponse
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r, err := e.ident.AcceptInvitation(ctx, &plansv1.AcceptInvitationRequest{Token: tok, Password: goodPassword, DisplayName: "Ned", Locale: "de"}); err == nil {
					mu.Lock()
					wins = append(wins, r)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if len(wins) != 1 {
			t.Fatalf("an invitation is single use: %d accepted", len(wins))
		}
		got := wins[0]
		if got.GetUser().GetEmail() != addr || !got.GetUser().GetEmailVerified() || len(got.GetMemberships()) != 1 || got.GetMemberships()[0].GetAccountId() != acct || got.GetMemberships()[0].GetRole() != "viewer" {
			t.Fatalf("accepted = %v", got)
		}
		if s, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: got.GetToken()}); err != nil || s.GetUser().GetEmail() != addr {
			t.Fatalf("the session works: %v, %v", s, err)
		}
		var locale string
		if err := e.system.QueryRow(ctx, `SELECT locale FROM users WHERE id = $1::uuid`, got.GetUser().GetId()).Scan(&locale); err != nil || locale != "de" {
			t.Errorf("locale = %q, %v", locale, err)
		}
		if _, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword}); err != nil {
			t.Errorf("the new user signs in with the chosen password: %v", err)
		}
		_, err = e.ident.AcceptInvitation(ctx, &plansv1.AcceptInvitationRequest{Token: tok, Password: goodPassword})
		wantCode(t, "second use", err, codes.InvalidArgument, "identity.token.invalid")
	})

	t.Run("accepting as an existing user", func(t *testing.T) {
		if _, err := e.invite(olga, acct, otto.email, "admin"); err != nil {
			t.Fatal(err)
		}
		tok := e.mail.lastToken(usermail.KindInvite, otto.email)
		l, err := e.ident.LookupInvitation(ctx, &plansv1.LookupInvitationRequest{Token: tok})
		if err != nil || !l.GetHasAccount() {
			t.Fatalf("lookup = %v, %v", l, err)
		}
		_, err = e.ident.AcceptInvitation(ctx, &plansv1.AcceptInvitationRequest{Token: tok, Password: goodPassword})
		wantCode(t, "creating a user for an address that has one", err, codes.FailedPrecondition, "identity.invite.sign_in_required")
		// Somebody else's account cannot use the token, and the failed try does not spend it.
		_, err = e.ident.AcceptInvitationAsUser(ctx, &plansv1.AcceptInvitationAsUserRequest{UserId: olga.id, Token: tok})
		wantCode(t, "another user", err, codes.PermissionDenied, "identity.invite.wrong_user")
		if r := e.roleOf(t, acct, otto.id); r != "" {
			t.Fatalf("Otto is not a member yet, role %q", r)
		}
		got, err := e.ident.AcceptInvitationAsUser(ctx, &plansv1.AcceptInvitationAsUserRequest{UserId: otto.id, Token: tok})
		if err != nil || got.GetAccountId() != acct || len(got.GetMemberships()) != 2 {
			t.Fatalf("accept as user = %v, %v", got, err)
		}
		if e.roleOf(t, acct, otto.id) != "admin" {
			t.Errorf("Otto's role = %q", e.roleOf(t, acct, otto.id))
		}
		_, err = e.ident.AcceptInvitationAsUser(ctx, &plansv1.AcceptInvitationAsUserRequest{UserId: otto.id, Token: tok})
		wantCode(t, "second use", err, codes.InvalidArgument, "identity.token.invalid")
		// An unverified user accepting an invitation sent to their address proves it.
		unv := e.signUp(t, "proof@members.example", "Proof Co")
		if _, err := e.invite(olga, acct, "proof@members.example", "viewer"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ident.AcceptInvitationAsUser(ctx, &plansv1.AcceptInvitationAsUserRequest{UserId: unv.GetUser().GetId(), Token: e.mail.lastToken(usermail.KindInvite, "proof@members.example")}); err != nil {
			t.Fatal(err)
		}
		s, _ := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: unv.GetToken()})
		if !s.GetUser().GetEmailVerified() {
			t.Error("accepting a mailed invitation proves the address")
		}
		// Otto left his own account's tenants alone: his own account still has its own apps and he is still its owner.
		if e.roleOf(t, otto.account, otto.id) != "owner" {
			t.Error("accepting does not touch the other account")
		}
	})

	t.Run("the account keeps its owner", func(t *testing.T) { testLastOwner(t, e, olga, adam, vera) })
	t.Run("invitation quotas", func(t *testing.T) { testInviteQuotas(t, e) })
	t.Run("rename", func(t *testing.T) {
		admin := e.joinNew(t, olga, "rename-admin@members.example", "admin")
		_, err := e.ident.RenameMyAccount(ctx, &plansv1.RenameMyAccountRequest{UserId: admin.id, AccountId: acct, Name: "Hijack"})
		wantCode(t, "admin renames", err, codes.PermissionDenied, "identity.account.forbidden")
		stranger := e.verified(t, "renamer@members.example", "Renamer Co")
		_, err = e.ident.RenameMyAccount(ctx, &plansv1.RenameMyAccountRequest{UserId: stranger.id, AccountId: acct, Name: "x"})
		wantCode(t, "a stranger renames", err, codes.NotFound, "identity.account.not_found")
		_, err = e.ident.RenameMyAccount(ctx, &plansv1.RenameMyAccountRequest{UserId: olga.id, AccountId: acct, Name: "  "})
		wantCode(t, "empty name", err, codes.InvalidArgument, "")
		r, err := e.ident.RenameMyAccount(ctx, &plansv1.RenameMyAccountRequest{UserId: olga.id, AccountId: acct, Name: " Olga Holdings "})
		if err != nil || r.GetName() != "Olga Holdings" {
			t.Fatalf("rename = %v, %v", r, err)
		}
		a, _ := e.ident.GetMyAccount(ctx, &plansv1.GetMyAccountRequest{UserId: olga.id, AccountId: acct})
		if a.GetName() != "Olga Holdings" {
			t.Errorf("name = %q", a.GetName())
		}
	})
}

// joinNew invites an address into the owner's account and accepts as a new user; it returns the new member.
func (e *env) joinNew(t *testing.T, owner person, email, role string) person {
	t.Helper()
	if _, err := e.invite(owner, owner.account, email, role); err != nil {
		t.Fatalf("invite %s: %v", email, err)
	}
	r, err := e.ident.AcceptInvitation(context.Background(), &plansv1.AcceptInvitationRequest{Token: e.mail.lastToken(usermail.KindInvite, email), Password: goodPassword})
	if err != nil {
		t.Fatalf("accept %s: %v", email, err)
	}
	return person{id: r.GetUser().GetId(), email: email, account: owner.account}
}

func testLastOwner(t *testing.T, e *env, olga, adam, vera person) {
	ctx := context.Background()
	acct := olga.account
	setRole := func(by person, target person, role string) error {
		_, err := e.ident.UpdateMemberRole(ctx, &plansv1.UpdateMemberRoleRequest{UserId: by.id, AccountId: acct, MemberUserId: target.id, Role: role})
		return err
	}
	remove := func(by person, target person) error {
		_, err := e.ident.RemoveMember(ctx, &plansv1.RemoveMemberRequest{UserId: by.id, AccountId: acct, MemberUserId: target.id})
		return err
	}

	// Roles.
	wantCode(t, "viewer changes a role", setRole(vera, adam, "viewer"), codes.PermissionDenied, "identity.account.forbidden")
	wantCode(t, "admin promotes to owner", setRole(adam, vera, "owner"), codes.PermissionDenied, "identity.account.forbidden")
	wantCode(t, "admin demotes the owner", setRole(adam, olga, "viewer"), codes.PermissionDenied, "identity.account.forbidden")
	wantCode(t, "bad role", setRole(olga, vera, "god"), codes.InvalidArgument, "")
	wantCode(t, "unknown member", setRole(olga, person{id: "00000000-0000-4000-8000-000000000000"}, "viewer"), codes.NotFound, "identity.member.not_found")
	wantCode(t, "malformed member id", setRole(olga, person{id: "nope"}, "viewer"), codes.NotFound, "identity.member.not_found")
	if err := setRole(adam, vera, "admin"); err != nil {
		t.Fatalf("an admin makes a viewer an admin: %v", err)
	}
	if err := setRole(adam, vera, "viewer"); err != nil {
		t.Fatal(err)
	}
	stranger := e.verified(t, "stranger@members.example", "Stranger Co")
	wantCode(t, "a stranger changes a role", setRole(stranger, vera, "owner"), codes.NotFound, "identity.account.not_found")
	wantCode(t, "a stranger removes a member", remove(stranger, vera), codes.NotFound, "identity.account.not_found")
	if e.roleOf(t, acct, vera.id) != "viewer" {
		t.Fatal("Vera's role is untouched by the strangers")
	}

	// The last owner.
	var owners []string
	rows, _ := e.system.Query(ctx, `SELECT user_id::text FROM account_members WHERE account_id = $1::uuid AND role = 'owner'`, acct)
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		owners = append(owners, id)
	}
	rows.Close()
	if len(owners) != 1 || owners[0] != olga.id {
		t.Fatalf("Olga should be the only owner, got %v", owners)
	}
	wantCode(t, "demoting the only owner", setRole(olga, olga, "admin"), codes.FailedPrecondition, "identity.member.last_owner")
	wantCode(t, "the only owner leaves", remove(olga, olga), codes.FailedPrecondition, "identity.member.last_owner")
	if e.roleOf(t, acct, olga.id) != "owner" {
		t.Fatal("the owner is still the owner")
	}

	// Two owners may each demote or remove the other, but not both at once.
	if err := setRole(olga, adam, "owner"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0] = setRole(olga, adam, "viewer") }()
	go func() { defer wg.Done(); results[1] = setRole(adam, olga, "viewer") }()
	wg.Wait()
	// Both asked to demote the other. Whatever the interleaving, exactly one request goes through (the second is refused: its author is no longer
	// allowed to, or is the last owner) and the account keeps an owner.
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		} else if code(t, err) != codes.FailedPrecondition && code(t, err) != codes.PermissionDenied {
			t.Errorf("the loser of a demotion race: %v", err)
		}
	}
	var n int
	if err := e.system.QueryRow(ctx, `SELECT count(*) FROM account_members WHERE account_id = $1::uuid AND role = 'owner'`, acct).Scan(&n); err != nil || n != 1 || wins != 1 {
		t.Fatalf("one demotion wins and the account keeps one owner: %d owners, %d wins, %v", n, wins, err)
	}

	// Put Olga back as the sole owner for the removal rules.
	if _, err := e.system.Exec(ctx, `UPDATE account_members SET role = CASE WHEN user_id = $2::uuid THEN 'owner' ELSE role END WHERE account_id = $1::uuid`, acct, olga.id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.system.Exec(ctx, `UPDATE account_members SET role = 'admin' WHERE account_id = $1::uuid AND user_id = $2::uuid`, acct, adam.id); err != nil {
		t.Fatal(err)
	}

	// Removal.
	wantCode(t, "viewer removes an admin", remove(vera, adam), codes.PermissionDenied, "identity.account.forbidden")
	wantCode(t, "admin removes the owner", remove(adam, olga), codes.PermissionDenied, "identity.account.forbidden")
	if err := remove(vera, vera); err != nil {
		t.Fatalf("a viewer may leave: %v", err)
	}
	if e.roleOf(t, acct, vera.id) != "" {
		t.Fatal("Vera left")
	}
	_, err := e.ident.GetMyAccount(ctx, &plansv1.GetMyAccountRequest{UserId: vera.id, AccountId: acct})
	wantCode(t, "a member who left reads the account", err, codes.NotFound, "identity.account.not_found")
	wantCode(t, "removing somebody who is not a member", remove(olga, vera), codes.NotFound, "identity.member.not_found")
	if err := remove(olga, adam); err != nil {
		t.Fatalf("an owner removes an admin: %v", err)
	}
	// A removed member reaches nothing of the account, apps and tenants included.
	app, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: olga.id, AccountId: acct, Name: "Members app", Currency: "USD", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: adam.id, AppId: app.GetApp().GetId()})
	wantCode(t, "a removed member reads an app", err, codes.NotFound, "identity.app.not_found")
	_, err = e.ident.ResolveMyTenant(ctx, &plansv1.ResolveMyTenantRequest{UserId: adam.id, TenantId: app.GetApp().GetTenants()[0].GetId()})
	wantCode(t, "a removed member acts on a tenant", err, codes.NotFound, "identity.tenant.not_found")
}

func testInviteQuotas(t *testing.T, e *env) {
	ctx := context.Background()
	q := e.verified(t, "quota-owner@members.example", "Quota Co")
	for i := 0; i < 30; i++ {
		if _, err := e.system.Exec(ctx, `INSERT INTO account_invites (id, account_id, email, role, invited_by, token_hash, expires_at, revoked_at)
			VALUES (gen_random_uuid(), $1::uuid, $2, 'viewer', $3::uuid, sha256(gen_random_uuid()::text::bytea), now() + interval '7 days', now())`,
			q.account, "bulk"+string(rune('a'+i%26))+string(rune('a'+i/26))+"@members.example", q.id); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.invite(q, q.account, "one-too-many@members.example", "viewer")
	wantCode(t, "more than 30 invitations in a day", err, codes.ResourceExhausted, "identity.invite.quota")
}
