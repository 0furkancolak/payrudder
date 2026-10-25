//go:build integration

package itest

import (
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
)

const newPassword = "another long password"

// testEmailVerificationAndReset covers the two mailed-token flows of phase 39: the token is random, only its hash is stored, it works once, expires, and
// the answers never tell whether an address has an account.
func testEmailVerificationAndReset(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	const addr = "verify-ada@example.com"

	if _, err := e.ident.SignUp(ctx, &plansv1.SignUpRequest{Email: addr, Password: goodPassword, AccountName: "Verify Co", Locale: "tr"}); err != nil {
		t.Fatal(err)
	}
	mails := e.mail.to(usermail.KindVerify, addr)
	if len(mails) != 1 || mails[0].Locale != "tr" || !strings.HasPrefix(mails[0].Link, testAppURL+"/verify#token=qv_") {
		t.Fatalf("verification mail = %+v", mails)
	}
	token := e.mail.lastToken(usermail.KindVerify, addr)

	in, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword})
	if err != nil || in.GetUser().GetEmailVerified() {
		t.Fatalf("an unverified user can sign in and is marked unverified: %v, %v", in, err)
	}
	uid := in.GetUser().GetId()

	t.Run("the token is stored as a hash with the documented lifetime", func(t *testing.T) {
		var stored []byte
		var ttl time.Duration
		if err := e.system.QueryRow(ctx, `SELECT token_hash, expires_at - created_at FROM user_tokens WHERE user_id = $1::uuid AND kind = 'verify_email'`, uid).Scan(&stored, &ttl); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(token))
		if string(stored) != string(sum[:]) {
			t.Error("the stored value is not the SHA-256 of the mailed token")
		}
		if ttl < 23*time.Hour+59*time.Minute || ttl > 24*time.Hour+time.Minute {
			t.Errorf("a verification link lives 24 hours, got %v", ttl)
		}
	})

	t.Run("a resend within a minute is refused, a later one replaces the link", func(t *testing.T) {
		_, err := e.ident.ResendVerification(ctx, &plansv1.ResendVerificationRequest{UserId: uid})
		wantCode(t, "resend right after sign-up", err, codes.ResourceExhausted, "identity.verify.throttled")
		if _, err := e.system.Exec(ctx, `UPDATE user_tokens SET created_at = now() - interval '2 minutes' WHERE user_id = $1::uuid`, uid); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ident.ResendVerification(ctx, &plansv1.ResendVerificationRequest{UserId: uid}); err != nil {
			t.Fatalf("resend after a minute: %v", err)
		}
		fresh := e.mail.lastToken(usermail.KindVerify, addr)
		if fresh == "" || fresh == token {
			t.Fatal("a resend mails a new token")
		}
		_, err = e.ident.VerifyEmail(ctx, &plansv1.VerifyEmailRequest{Token: token})
		wantCode(t, "the replaced link", err, codes.InvalidArgument, "identity.token.invalid")
		token = fresh
	})

	t.Run("garbage, other kinds and expired tokens are all the same refusal", func(t *testing.T) {
		for name, bad := range map[string]string{"empty": "", "unknown": "qv_" + strings.Repeat("A", 43), "wrong prefix": "qr_" + strings.Repeat("A", 43), "session token": in.GetToken()} {
			_, err := e.ident.VerifyEmail(ctx, &plansv1.VerifyEmailRequest{Token: bad})
			wantCode(t, name, err, codes.InvalidArgument, "identity.token.invalid")
		}
		if _, err := e.system.Exec(ctx, `UPDATE user_tokens SET expires_at = now() - interval '1 second' WHERE user_id = $1::uuid AND used_at IS NULL`, uid); err != nil {
			t.Fatal(err)
		}
		_, err := e.ident.VerifyEmail(ctx, &plansv1.VerifyEmailRequest{Token: token})
		wantCode(t, "expired", err, codes.InvalidArgument, "identity.token.invalid")
		if _, err := e.system.Exec(ctx, `UPDATE user_tokens SET expires_at = now() + interval '1 hour' WHERE user_id = $1::uuid AND used_at IS NULL`, uid); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("the link verifies once", func(t *testing.T) {
		got, err := e.ident.VerifyEmail(ctx, &plansv1.VerifyEmailRequest{Token: token})
		if err != nil || !got.GetUser().GetEmailVerified() {
			t.Fatalf("VerifyEmail = %v, %v", got, err)
		}
		_, err = e.ident.VerifyEmail(ctx, &plansv1.VerifyEmailRequest{Token: token})
		wantCode(t, "second use", err, codes.InvalidArgument, "identity.token.invalid")
		s, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: in.GetToken()})
		if err != nil || !s.GetUser().GetEmailVerified() {
			t.Fatalf("the open session sees the verification: %v, %v", s, err)
		}
		before := len(e.mail.to(usermail.KindVerify, addr))
		if _, err := e.ident.ResendVerification(ctx, &plansv1.ResendVerificationRequest{UserId: uid}); err != nil || len(e.mail.to(usermail.KindVerify, addr)) != before {
			t.Errorf("resend for a verified address does nothing: %v", err)
		}
	})

	t.Run("two requests with one verification token: one wins", func(t *testing.T) {
		const other = "verify-race@example.com"
		if _, err := e.ident.SignUp(ctx, &plansv1.SignUpRequest{Email: other, Password: goodPassword, AccountName: "Race Co"}); err != nil {
			t.Fatal(err)
		}
		tok := e.mail.lastToken(usermail.KindVerify, other)
		wins := 0
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.ident.VerifyEmail(ctx, &plansv1.VerifyEmailRequest{Token: tok}); err == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Errorf("a token is single use: %d requests succeeded", wins)
		}
	})

	t.Run("password reset", func(t *testing.T) { testPasswordReset(t, e, addr, in) })
}

func testPasswordReset(t *testing.T, e *env, addr string, session *plansv1.SignInResponse) {
	ctx := context.Background()
	uid := session.GetUser().GetId()

	// An unknown address and a known one get the same answer; only the known one gets a mail.
	for _, a := range []string{"nobody-at-all@example.com", addr} {
		if _, err := e.ident.RequestPasswordReset(ctx, &plansv1.RequestPasswordResetRequest{Email: a}); err != nil {
			t.Fatalf("RequestPasswordReset(%s): %v", a, err)
		}
	}
	if n := len(e.mail.to(usermail.KindReset, "nobody-at-all@example.com")); n != 0 {
		t.Errorf("no mail to an address without an account, got %d", n)
	}
	if n := len(e.mail.to(usermail.KindReset, addr)); n != 1 {
		t.Fatalf("one reset mail to the owner, got %d", n)
	}
	if got := e.mail.to(usermail.KindReset, addr)[0]; got.Locale != "tr" || !strings.HasPrefix(got.Link, testAppURL+"/reset#token=qr_") {
		t.Errorf("reset mail = %+v", got)
	}
	_, err := e.ident.RequestPasswordReset(ctx, &plansv1.RequestPasswordResetRequest{Email: "not an address"})
	wantCode(t, "malformed address", err, codes.InvalidArgument, "")
	// Asking again within a minute answers the same and sends nothing more.
	if _, err := e.ident.RequestPasswordReset(ctx, &plansv1.RequestPasswordResetRequest{Email: strings.ToUpper(addr)}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.mail.to(usermail.KindReset, addr)); n != 1 {
		t.Errorf("reset mails are throttled to one a minute, got %d", n)
	}
	token := e.mail.lastToken(usermail.KindReset, addr)
	var ttl time.Duration
	if err := e.system.QueryRow(ctx, `SELECT expires_at - created_at FROM user_tokens WHERE user_id = $1::uuid AND kind = 'reset_password' AND used_at IS NULL`, uid).Scan(&ttl); err != nil {
		t.Fatal(err)
	}
	if ttl < 59*time.Minute || ttl > 61*time.Minute {
		t.Errorf("a reset link lives an hour, got %v", ttl)
	}

	// Refusals, none of which uses the token up.
	_, err = e.ident.ResetPassword(ctx, &plansv1.ResetPasswordRequest{Token: token, NewPassword: "short"})
	wantCode(t, "short new password", err, codes.InvalidArgument, "")
	for name, bad := range map[string]string{"unknown": "qr_" + strings.Repeat("B", 43), "a verification token": "qv_" + strings.Repeat("B", 43), "empty": ""} {
		_, err = e.ident.ResetPassword(ctx, &plansv1.ResetPasswordRequest{Token: bad, NewPassword: newPassword})
		wantCode(t, name, err, codes.InvalidArgument, "identity.token.invalid")
	}
	if _, err := e.system.Exec(ctx, `UPDATE user_tokens SET expires_at = now() - interval '1 second' WHERE user_id = $1::uuid AND kind = 'reset_password' AND used_at IS NULL`, uid); err != nil {
		t.Fatal(err)
	}
	_, err = e.ident.ResetPassword(ctx, &plansv1.ResetPasswordRequest{Token: token, NewPassword: newPassword})
	wantCode(t, "expired reset", err, codes.InvalidArgument, "identity.token.invalid")
	if _, err := e.system.Exec(ctx, `UPDATE user_tokens SET expires_at = now() + interval '1 hour' WHERE user_id = $1::uuid AND kind = 'reset_password' AND used_at IS NULL`, uid); err != nil {
		t.Fatal(err)
	}
	// A locked account and a second open session, both gone after the reset.
	for i := 0; i < 5; i++ {
		_, _ = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: "wrong password!"})
	}
	if _, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword}); err == nil {
		t.Fatal("the account should be locked")
	}

	// Two requests with the same token: one wins.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.ident.ResetPassword(ctx, &plansv1.ResetPasswordRequest{Token: token, NewPassword: newPassword}); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("a reset token is single use: %d requests succeeded", wins)
	}
	_, err = e.ident.ResetPassword(ctx, &plansv1.ResetPasswordRequest{Token: token, NewPassword: "yet another password"})
	wantCode(t, "reusing the reset token", err, codes.InvalidArgument, "identity.token.invalid")

	// The session from before is revoked, the old password is dead, the new one works (the lock is cleared too), the address counts as proven.
	_, err = e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: session.GetToken()})
	wantCode(t, "session open before the reset", err, codes.Unauthenticated, "identity.session.invalid")
	_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword})
	wantCode(t, "old password", err, codes.Unauthenticated, "identity.auth.invalid_credentials")
	back, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: newPassword})
	if err != nil || !back.GetUser().GetEmailVerified() {
		t.Fatalf("sign-in with the new password: %v, %v", back, err)
	}

	// A reset is how an unverified person who holds the mailbox proves it, and how a squatter who registered the address first is dislodged.
	const squatted = "squatted@example.com"
	if _, err := e.ident.SignUp(ctx, &plansv1.SignUpRequest{Email: squatted, Password: goodPassword, AccountName: "Squatter"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ident.RequestPasswordReset(ctx, &plansv1.RequestPasswordResetRequest{Email: squatted, Locale: "de"}); err != nil {
		t.Fatal(err)
	}
	if m := e.mail.to(usermail.KindReset, squatted); len(m) != 1 || m[0].Locale != "de" {
		t.Fatalf("the reset mail follows the language of the request: %+v", m)
	}
	if _, err := e.ident.ResetPassword(ctx, &plansv1.ResetPasswordRequest{Token: e.mail.lastToken(usermail.KindReset, squatted), NewPassword: newPassword}); err != nil {
		t.Fatal(err)
	}
	r, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: squatted, Password: newPassword})
	if err != nil || !r.GetUser().GetEmailVerified() {
		t.Fatalf("a reset proves the address: %v, %v", r, err)
	}
}
