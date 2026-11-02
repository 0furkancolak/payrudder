//go:build integration

package itest

import (
	"context"
	"encoding/base32"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/totp"
)

// codeNow is the code the authenticator app shows now.
func codeNow(t *testing.T, secret string) string {
	t.Helper()
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return totp.Code(raw, totp.Step(time.Now()))
}

// fresh forgets the last step a code was accepted for, so the code of this step can be used again (the test clock is the real one, and a step lasts 30 seconds).
func (e *env) fresh(t *testing.T, userID string) {
	t.Helper()
	if _, err := e.system.Exec(context.Background(), `UPDATE user_totp SET last_step = 0 WHERE user_id = $1::uuid`, userID); err != nil {
		t.Fatal(err)
	}
}

func (e *env) failures(t *testing.T, userID string) int {
	t.Helper()
	var n int
	if err := e.system.QueryRow(context.Background(), `SELECT failed_logins FROM users WHERE id = $1::uuid`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// enableTwoFactor turns it on for a person and returns their secret and recovery codes.
func (e *env) enableTwoFactor(t *testing.T, p person) (secret string, recovery []string) {
	t.Helper()
	ctx := context.Background()
	setup, err := e.ident.SetupTwoFactor(ctx, &plansv1.SetupTwoFactorRequest{UserId: p.id})
	if err != nil {
		t.Fatal(err)
	}
	done, err := e.ident.ConfirmTwoFactor(ctx, &plansv1.ConfirmTwoFactorRequest{UserId: p.id, Code: codeNow(t, setup.GetSecret())})
	if err != nil {
		t.Fatal(err)
	}
	return setup.GetSecret(), done.GetRecoveryCodes()
}

// firstStep is the password step of a sign-in.
func (e *env) firstStep(t *testing.T, email string) *plansv1.SignInResponse {
	t.Helper()
	r, err := e.ident.SignIn(context.Background(), &plansv1.SignInRequest{Email: email, Password: goodPassword})
	if err != nil {
		t.Fatalf("sign in %s: %v", email, err)
	}
	return r
}

// testTwoFactor: TOTP and recovery codes, the sign-in step that follows the password, the mailed code and the identity provider, the lock and the tries.
func testTwoFactor(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	olga := e.verified(t, "olga@twofactor.example", "Two Factor Co")

	t.Run("setup and confirm", func(t *testing.T) {
		st, err := e.ident.GetTwoFactor(ctx, &plansv1.GetTwoFactorRequest{UserId: olga.id})
		if err != nil || st.GetEnabled() || st.GetSetupRequired() {
			t.Fatalf("before: %v %v", st, err)
		}
		first, err := e.ident.SetupTwoFactor(ctx, &plansv1.SetupTwoFactorRequest{UserId: olga.id})
		if err != nil || !strings.HasPrefix(first.GetOtpauthUri(), "otpauth://totp/Payrudder:olga@twofactor.example?") || !strings.Contains(first.GetOtpauthUri(), "secret="+first.GetSecret()) {
			t.Fatalf("setup = %v, %v", first, err)
		}
		// Not on yet: a sign-in still opens a session, and a second setup replaces the pending secret.
		if r := e.firstStep(t, olga.email); r.GetToken() == "" || r.GetChallenge() != "" {
			t.Fatalf("a pending setup must not change sign-in: %v", r)
		}
		second, err := e.ident.SetupTwoFactor(ctx, &plansv1.SetupTwoFactorRequest{UserId: olga.id})
		if err != nil || second.GetSecret() == first.GetSecret() {
			t.Fatalf("second setup = %v, %v", second, err)
		}
		_, err = e.ident.ConfirmTwoFactor(ctx, &plansv1.ConfirmTwoFactorRequest{UserId: olga.id, Code: codeNow(t, first.GetSecret())})
		wantCode(t, "a code of the replaced secret", err, codes.InvalidArgument, "identity.two_factor.bad_code")
		_, err = e.ident.ConfirmTwoFactor(ctx, &plansv1.ConfirmTwoFactorRequest{UserId: olga.id, Code: "12ab56"})
		wantCode(t, "a code that is not six digits", err, codes.InvalidArgument, "")
		done, err := e.ident.ConfirmTwoFactor(ctx, &plansv1.ConfirmTwoFactorRequest{UserId: olga.id, Code: codeNow(t, second.GetSecret())})
		if err != nil || len(done.GetRecoveryCodes()) != totp.RecoveryCodes {
			t.Fatalf("confirm = %v, %v", done, err)
		}
		st, _ = e.ident.GetTwoFactor(ctx, &plansv1.GetTwoFactorRequest{UserId: olga.id})
		if !st.GetEnabled() || st.GetEnableTime() == nil || st.GetRecoveryCodesRemaining() != totp.RecoveryCodes {
			t.Fatalf("after: %v", st)
		}
		_, err = e.ident.SetupTwoFactor(ctx, &plansv1.SetupTwoFactorRequest{UserId: olga.id})
		wantCode(t, "setup while it is on", err, codes.FailedPrecondition, "identity.two_factor.already_enabled")
		_, err = e.ident.ConfirmTwoFactor(ctx, &plansv1.ConfirmTwoFactorRequest{UserId: olga.id, Code: codeNow(t, second.GetSecret())})
		wantCode(t, "confirm while it is on", err, codes.FailedPrecondition, "identity.two_factor.already_enabled")

		// The database holds neither the secret nor a recovery code in the clear.
		var sealed []byte
		var hashes int
		if err := e.system.QueryRow(ctx, `SELECT secret_enc FROM user_totp WHERE user_id = $1::uuid`, olga.id).Scan(&sealed); err != nil || strings.Contains(string(sealed), second.GetSecret()) {
			t.Fatalf("stored secret: %v", err)
		}
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM user_recovery_codes WHERE user_id = $1::uuid AND code_hash = ANY($2)`, olga.id, [][]byte{[]byte(done.GetRecoveryCodes()[0])}).Scan(&hashes); err != nil || hashes != 0 {
			t.Fatalf("a recovery code is stored in the clear: %d, %v", hashes, err)
		}
		me, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: e.firstStepToken(t, olga.email, second.GetSecret())})
		if err != nil || !me.GetUser().GetTwoFactorEnabled() || me.GetUser().GetTwoFactorSetupRequired() {
			t.Fatalf("session user = %v, %v", me.GetUser(), err)
		}
	})

	var secret string
	var recovery []string
	t.Run("the second step", func(t *testing.T) {
		// A fresh person, so the numbers of failures start from zero.
		ada := e.verified(t, "ada@twofactor.example", "Ada Co")
		secret, recovery = e.enableTwoFactor(t, ada)

		r := e.firstStep(t, ada.email)
		if r.GetToken() != "" || r.GetUser() != nil || !strings.HasPrefix(r.GetChallenge(), "qc_") || r.GetChallengeExpireTime() == nil {
			t.Fatalf("the password step with two-factor on must give a challenge and no session: %v", r)
		}
		if n := e.failures(t, ada.id); n != 0 {
			t.Errorf("a right password that waits for the second step must not count as a failure, got %d", n)
		}
		var stored []byte
		if err := e.system.QueryRow(ctx, `SELECT token_hash FROM login_challenges WHERE user_id = $1::uuid`, ada.id).Scan(&stored); err != nil || strings.Contains(string(stored), r.GetChallenge()) {
			t.Fatalf("the challenge must be stored as a hash: %v", err)
		}

		// The second step: a wrong code counts; the right one opens the session; the same code does not work twice.
		_, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: r.GetChallenge(), Code: "000000"})
		wantCode(t, "a wrong code", err, codes.Unauthenticated, "identity.two_factor.invalid")
		if n := e.failures(t, ada.id); n != 1 {
			t.Errorf("failures after a wrong code = %d", n)
		}
		e.fresh(t, ada.id)
		code := codeNow(t, secret)
		opened, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: r.GetChallenge(), Code: code})
		if err != nil || !strings.HasPrefix(opened.GetToken(), "qs_") || opened.GetUser().GetEmail() != ada.email || len(opened.GetMemberships()) != 1 {
			t.Fatalf("verify = %v, %v", opened, err)
		}
		if n := e.failures(t, ada.id); n != 0 {
			t.Errorf("a sign-in resets the failures, got %d", n)
		}
		_, err = e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: r.GetChallenge(), Code: code})
		wantCode(t, "a spent challenge", err, codes.Unauthenticated, "identity.two_factor.invalid")
		again := e.firstStep(t, ada.email)
		_, err = e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: again.GetChallenge(), Code: code})
		wantCode(t, "the same code twice", err, codes.Unauthenticated, "identity.two_factor.invalid")
		_, err = e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: "qc_" + strings.Repeat("a", 43), Code: code})
		wantCode(t, "an unknown challenge", err, codes.Unauthenticated, "identity.two_factor.invalid")
		_, err = e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: "nonsense", Code: code})
		wantCode(t, "a malformed challenge", err, codes.Unauthenticated, "identity.two_factor.invalid")
		if _, err := e.system.Exec(ctx, `UPDATE login_challenges SET expires_at = now() - interval '1 second' WHERE user_id = $1::uuid`, ada.id); err != nil {
			t.Fatal(err)
		}
		e.fresh(t, ada.id)
		_, err = e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: again.GetChallenge(), Code: codeNow(t, secret)})
		wantCode(t, "an expired challenge", err, codes.Unauthenticated, "identity.two_factor.invalid")
	})

	t.Run("recovery codes work once", func(t *testing.T) {
		ada := person{email: "ada@twofactor.example"}
		var id string
		if err := e.system.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, ada.email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		_ = e.system.QueryRow(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL WHERE id = $1::uuid RETURNING 1`, id).Scan(new(int))
		r := e.firstStep(t, ada.email)
		typed := strings.ToUpper(strings.Replace(recovery[0], "-", " ", 1)) // how a person might type it
		opened, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: r.GetChallenge(), Code: typed})
		if err != nil || opened.GetToken() == "" {
			t.Fatalf("recovery code: %v %v", opened, err)
		}
		again := e.firstStep(t, ada.email)
		_, err = e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: again.GetChallenge(), Code: recovery[0]})
		wantCode(t, "the same recovery code twice", err, codes.Unauthenticated, "identity.two_factor.invalid")
		st, _ := e.ident.GetTwoFactor(ctx, &plansv1.GetTwoFactorRequest{UserId: id})
		if st.GetRecoveryCodesRemaining() != totp.RecoveryCodes-1 {
			t.Errorf("recovery codes remaining = %d", st.GetRecoveryCodesRemaining())
		}
		evs := e.events(t, operator(), &auditv1.ListAuditEventsRequest{Actor: ada.email, Actions: []string{"two_factor.*"}, PageSize: 200})
		if has(evs, "two_factor.enabled") == nil || has(evs, "two_factor.recovery_code_used") == nil {
			t.Errorf("the audit log of the person's sign-in security: %v", actions(evs))
		}
		for _, ev := range evs {
			if strings.Contains(ev.GetDetailJson()+ev.GetSummary(), recovery[0]) || ev.GetTenantId() != "" {
				t.Errorf("entry %v holds the code or a tenant", ev)
			}
		}
	})

	t.Run("the lock and the tries", func(t *testing.T) {
		bo := e.verified(t, "bo@twofactor.example", "Bo Co")
		bsecret, _ := e.enableTwoFactor(t, bo)
		r := e.firstStep(t, bo.email)
		// Five wrong codes burn the challenge and lock the account like five wrong passwords.
		for i := range command.MaxChallengeAttempts {
			_, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: r.GetChallenge(), Code: "11111" + string(rune('0'+i))})
			wantCode(t, "a wrong code", err, codes.Unauthenticated, "identity.two_factor.invalid")
		}
		e.fresh(t, bo.id)
		_, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: r.GetChallenge(), Code: codeNow(t, bsecret)})
		wantCode(t, "the right code on a burned challenge", err, codes.Unauthenticated, "identity.two_factor.invalid")
		var locked bool
		if err := e.system.QueryRow(ctx, `SELECT locked_until > now() FROM users WHERE id = $1::uuid`, bo.id).Scan(&locked); err != nil || !locked {
			t.Fatalf("five wrong codes must lock the account: %v %v", locked, err)
		}
		_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: bo.email, Password: goodPassword})
		wantCode(t, "the right password on a locked account", err, codes.Unauthenticated, "identity.auth.invalid_credentials")
		// A challenge made before the lock is useless too, whatever the code.
		if _, err := e.system.Exec(ctx, `UPDATE users SET locked_until = NULL, failed_logins = 0 WHERE id = $1::uuid`, bo.id); err != nil {
			t.Fatal(err)
		}
		fresh := e.firstStep(t, bo.email)
		if _, err := e.system.Exec(ctx, `UPDATE users SET locked_until = now() + interval '15 minutes', failed_logins = 5 WHERE id = $1::uuid`, bo.id); err != nil {
			t.Fatal(err)
		}
		_, err = e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: fresh.GetChallenge(), Code: codeNow(t, bsecret)})
		wantCode(t, "the right code on a locked account", err, codes.Unauthenticated, "identity.two_factor.invalid")
	})

	t.Run("the mailed code and the identity provider", func(t *testing.T) {
		cy := e.verified(t, "cy@twofactor.example", "Cy Co")
		csecret, _ := e.enableTwoFactor(t, cy)
		if _, err := e.ident.RequestLoginCode(ctx, &plansv1.RequestLoginCodeRequest{Email: cy.email}); err != nil {
			t.Fatal(err)
		}
		viaCode, err := e.ident.VerifyLoginCode(ctx, &plansv1.VerifyLoginCodeRequest{Email: cy.email, Code: e.mail.lastCode(cy.email)})
		if err != nil || viaCode.GetToken() != "" || viaCode.GetChallenge() == "" {
			t.Fatalf("a mailed code with two-factor on must give a challenge: %v %v", viaCode, err)
		}
		viaGoogle, err := e.ident.SignInWithIdentity(ctx, &plansv1.SignInWithIdentityRequest{Provider: "google", Subject: "g-twofactor-cy", Email: cy.email, EmailVerified: true})
		if err != nil || viaGoogle.GetToken() != "" || viaGoogle.GetChallenge() == "" {
			t.Fatalf("Google with two-factor on must give a challenge: %v %v", viaGoogle, err)
		}
		e.fresh(t, cy.id)
		opened, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: viaGoogle.GetChallenge(), Code: codeNow(t, csecret)})
		if err != nil || opened.GetToken() == "" {
			t.Fatalf("second step after Google: %v %v", opened, err)
		}
		// Linked now: the next Google sign-in is found by its subject, and still waits for the second factor.
		if r, err := e.ident.SignInWithIdentity(ctx, &plansv1.SignInWithIdentityRequest{Provider: "google", Subject: "g-twofactor-cy", Email: cy.email, EmailVerified: true}); err != nil || r.GetChallenge() == "" {
			t.Fatalf("a linked identity: %v %v", r, err)
		}
		// A person without it is unchanged.
		if r, err := e.ident.SignInWithIdentity(ctx, &plansv1.SignInWithIdentityRequest{Provider: "google", Subject: "g-twofactor-new", Email: "new@twofactor.example", EmailVerified: true}); err != nil || r.GetToken() == "" || r.GetChallenge() != "" {
			t.Fatalf("a person without two-factor: %v %v", r, err)
		}
	})

	t.Run("turning it off and new recovery codes", func(t *testing.T) {
		di := e.verified(t, "di@twofactor.example", "Di Co")
		dsecret, drecovery := e.enableTwoFactor(t, di)
		_, err := e.ident.DisableTwoFactor(ctx, &plansv1.DisableTwoFactorRequest{UserId: di.id, Code: "000000"})
		wantCode(t, "disable with a wrong code", err, codes.InvalidArgument, "identity.two_factor.bad_code")
		e.fresh(t, di.id)
		fresh, err := e.ident.RegenerateRecoveryCodes(ctx, &plansv1.RegenerateRecoveryCodesRequest{UserId: di.id, Code: codeNow(t, dsecret)})
		if err != nil || len(fresh.GetRecoveryCodes()) != totp.RecoveryCodes || fresh.GetRecoveryCodes()[0] == drecovery[0] {
			t.Fatalf("regenerate = %v, %v", fresh, err)
		}
		_, err = e.ident.DisableTwoFactor(ctx, &plansv1.DisableTwoFactorRequest{UserId: di.id, Code: drecovery[1]})
		wantCode(t, "an old recovery code", err, codes.InvalidArgument, "identity.two_factor.bad_code")
		if _, err := e.ident.DisableTwoFactor(ctx, &plansv1.DisableTwoFactorRequest{UserId: di.id, Code: fresh.GetRecoveryCodes()[0]}); err != nil {
			t.Fatalf("disable with a recovery code: %v", err)
		}
		if r := e.firstStep(t, di.email); r.GetToken() == "" || r.GetChallenge() != "" {
			t.Fatalf("after turning it off a sign-in opens a session: %v", r)
		}
		_, err = e.ident.DisableTwoFactor(ctx, &plansv1.DisableTwoFactorRequest{UserId: di.id, Code: "000000"})
		wantCode(t, "disable when it is off", err, codes.FailedPrecondition, "identity.two_factor.not_enabled")
		var rows int
		if err := e.system.QueryRow(ctx, `SELECT (SELECT count(*) FROM user_totp WHERE user_id = $1::uuid) + (SELECT count(*) FROM user_recovery_codes WHERE user_id = $1::uuid)`, di.id).Scan(&rows); err != nil || rows != 0 {
			t.Errorf("secret and codes must be gone: %d, %v", rows, err)
		}
		evs := e.events(t, operator(), &auditv1.ListAuditEventsRequest{Actor: di.email, Actions: []string{"two_factor.*"}, PageSize: 200})
		for _, want := range []string{"two_factor.enabled", "two_factor.recovery_codes_replaced", "two_factor.disabled"} {
			if has(evs, want) == nil {
				t.Errorf("no %s entry: %v", want, actions(evs))
			}
		}
	})

	t.Run("owners can be required to have it", func(t *testing.T) {
		eve := e.verified(t, "eve@twofactor.example", "Eve Co")
		_, err := e.ident.SetAccountSecurity(ctx, &plansv1.SetAccountSecurityRequest{UserId: eve.id, AccountId: eve.account, RequireTwoFactorForOwners: true})
		wantCode(t, "requiring it without having it", err, codes.FailedPrecondition, "identity.two_factor.not_enabled")
		esecret, _ := e.enableTwoFactor(t, eve)
		fay := e.joinNew(t, eve, "fay@twofactor.example", "owner")
		gus := e.joinNew(t, eve, "gus@twofactor.example", "admin")
		if acc, _ := e.ident.GetMyAccount(ctx, &plansv1.GetMyAccountRequest{UserId: eve.id, AccountId: eve.account}); acc.GetRequireTwoFactorForOwners() {
			t.Fatal("the default is off")
		}
		_, err = e.ident.SetAccountSecurity(ctx, &plansv1.SetAccountSecurityRequest{UserId: gus.id, AccountId: eve.account, RequireTwoFactorForOwners: true})
		wantCode(t, "an administrator", err, codes.PermissionDenied, "identity.account.forbidden")
		on, err := e.ident.SetAccountSecurity(ctx, &plansv1.SetAccountSecurityRequest{UserId: eve.id, AccountId: eve.account, RequireTwoFactorForOwners: true})
		if err != nil || !on.GetRequireTwoFactorForOwners() {
			t.Fatalf("set: %v %v", on, err)
		}
		sessionOf := func(email string) *plansv1.User {
			t.Helper()
			r := e.firstStep(t, email)
			s, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: r.GetToken()})
			if err != nil {
				t.Fatal(err)
			}
			return s.GetUser()
		}
		if u := sessionOf(fay.email); !u.GetTwoFactorSetupRequired() || u.GetTwoFactorEnabled() {
			t.Errorf("an owner without it must be told to set it up: %v", u)
		}
		if u := sessionOf(gus.email); u.GetTwoFactorSetupRequired() {
			t.Errorf("an administrator is not asked: %v", u)
		}
		e.fresh(t, eve.id)
		_, err = e.ident.DisableTwoFactor(ctx, &plansv1.DisableTwoFactorRequest{UserId: eve.id, Code: codeNow(t, esecret)})
		wantCode(t, "turning it off while required", err, codes.PermissionDenied, "identity.two_factor.required_by_organisation")
		st, _ := e.ident.GetTwoFactor(ctx, &plansv1.GetTwoFactorRequest{UserId: fay.id})
		if !st.GetSetupRequired() || st.GetEnabled() {
			t.Errorf("status of the owner without it: %v", st)
		}
		fsecret, _ := e.enableTwoFactor(t, fay)
		_ = fsecret
		if st, _ := e.ident.GetTwoFactor(ctx, &plansv1.GetTwoFactorRequest{UserId: fay.id}); st.GetSetupRequired() || !st.GetRequiredByOrganisation() {
			t.Errorf("after setting it up: %v", st)
		}
		if _, err := e.ident.SetAccountSecurity(ctx, &plansv1.SetAccountSecurityRequest{UserId: eve.id, AccountId: eve.account, RequireTwoFactorForOwners: false}); err != nil {
			t.Fatal(err)
		}
		evs := e.events(t, operator(), &auditv1.ListAuditEventsRequest{AccountId: eve.account, Actions: []string{"account.*"}})
		if len(evs) != 2 || evs[0].GetAccountId() != eve.account || evs[0].GetTenantId() != "" {
			t.Errorf("policy entries = %v", evs)
		}
	})
}

// firstStepToken signs a person with two-factor on all the way in and returns the session token.
func (e *env) firstStepToken(t *testing.T, email, secret string) string {
	t.Helper()
	var id string
	if err := e.system.QueryRow(context.Background(), `SELECT id::text FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	r := e.firstStep(t, email)
	e.fresh(t, id)
	o, err := e.ident.VerifyTwoFactor(context.Background(), &plansv1.VerifyTwoFactorRequest{Challenge: r.GetChallenge(), Code: codeNow(t, secret)})
	if err != nil {
		t.Fatalf("second step for %s: %v", email, err)
	}
	return o.GetToken()
}
