//go:build integration

package itest

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/credential"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/usermail"
)

var sixDigits = regexp.MustCompile(`^[0-9]{6}$`)

// lastCode is the code in the newest sign-in code mail to an address ("" when none); the database holds only a keyed hash.
func (r *recordingMail) lastCode(address string) string {
	ms := r.to(usermail.KindCode, address)
	if len(ms) == 0 {
		return ""
	}
	return ms[len(ms)-1].Code
}

// age moves the codes of an address back in time, so the one-a-minute rule (and expiry) can be passed without sleeping.
func (e *env) ageCodes(t *testing.T, email string, by string) {
	t.Helper()
	if _, err := e.system.Exec(context.Background(), `UPDATE login_codes SET created_at = created_at - $2::interval, expires_at = expires_at - $2::interval WHERE email = $1`, email, by); err != nil {
		t.Fatal(err)
	}
}

func (e *env) requestCode(t *testing.T, email string) {
	t.Helper()
	if _, err := e.ident.RequestLoginCode(context.Background(), &plansv1.RequestLoginCodeRequest{Email: email, Locale: "de"}); err != nil {
		t.Fatalf("RequestLoginCode %s: %v", email, err)
	}
}

func (e *env) verifyCode(email, code string) (*plansv1.SignInResponse, error) {
	return e.ident.VerifyLoginCode(context.Background(), &plansv1.VerifyLoginCodeRequest{Email: email, Code: code, AccountName: "Code Co", DisplayName: "Coder"})
}

// testSignInMethods covers phase 41: the one-time code and the identity provider sign-in. (E-mail and password is testIdentity.)
func testSignInMethods(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	t.Run("a code is random, six digits, stored keyed and hashed, and lives ten minutes", func(t *testing.T) {
		const addr = "otp-store@example.com"
		e.requestCode(t, addr)
		code := e.mail.lastCode(addr)
		if !sixDigits.MatchString(code) {
			t.Fatalf("code %q is not six digits", code)
		}
		if m := e.mail.to(usermail.KindCode, addr)[0]; m.Locale != "de" || m.Link != "" {
			t.Errorf("mail = %+v: the language is the request's for an unknown address and a code mail has no link", m)
		}
		var salt, hash []byte
		var ttl time.Duration
		var attempts int
		if err := e.system.QueryRow(ctx, `SELECT salt, code_hash, expires_at - created_at, attempts FROM login_codes WHERE email = $1`, addr).Scan(&salt, &hash, &ttl, &attempts); err != nil {
			t.Fatal(err)
		}
		if !credential.EqualHash(hash, credential.HashCode([]byte(testPepper), salt, addr, code)) {
			t.Error("the stored value is not the keyed hash of the code")
		}
		if credential.EqualHash(hash, credential.HashCode(nil, salt, addr, code)) {
			t.Error("the hash must depend on the server's pepper")
		}
		if strings.Contains(string(hash), code) || ttl != 10*time.Minute || attempts != 0 {
			t.Errorf("hash=%x ttl=%v attempts=%d", hash, ttl, attempts)
		}
		// Codes are not a sequence: a handful of fresh ones are all different.
		seen := map[string]bool{code: true}
		for i := 0; i < 6; i++ {
			c, _, err := credential.NewCode()
			if err != nil || !sixDigits.MatchString(c) {
				t.Fatalf("NewCode = %q, %v", c, err)
			}
			seen[c] = true
		}
		if len(seen) < 5 {
			t.Errorf("codes repeat: %v", seen)
		}
	})

	t.Run("requests are throttled per address: one a minute, five an hour, without telling", func(t *testing.T) {
		const addr = "otp-throttle@example.com"
		e.requestCode(t, addr)
		e.requestCode(t, addr) // within the minute: answered, but nothing is sent
		if n := len(e.mail.to(usermail.KindCode, addr)); n != 1 {
			t.Fatalf("%d mails after two quick requests, want 1", n)
		}
		for i := 2; i <= 5; i++ {
			e.ageCodes(t, addr, "2 minutes")
			e.requestCode(t, addr)
		}
		if n := len(e.mail.to(usermail.KindCode, addr)); n != 5 {
			t.Fatalf("%d mails, want 5", n)
		}
		e.ageCodes(t, addr, "2 minutes")
		e.requestCode(t, addr) // the sixth in an hour: no error, no mail
		if n := len(e.mail.to(usermail.KindCode, addr)); n != 5 {
			t.Fatalf("the hourly cap did not hold: %d mails", n)
		}
		_, err := e.ident.RequestLoginCode(ctx, &plansv1.RequestLoginCodeRequest{Email: "not an address"})
		wantCode(t, "a malformed address", err, codes.InvalidArgument, "")
	})

	t.Run("the first code of an unknown address signs it up: user, organisation, verified, no password", func(t *testing.T) {
		const addr = "otp-new@example.com"
		e.requestCode(t, addr)
		in, err := e.verifyCode(addr, e.mail.lastCode(addr))
		if err != nil {
			t.Fatal(err)
		}
		if !in.GetUser().GetEmailVerified() || in.GetUser().GetEmail() != addr || len(in.GetMemberships()) != 1 || in.GetMemberships()[0].GetRole() != "owner" ||
			in.GetMemberships()[0].GetAccountName() != "Code Co" || in.GetUser().GetDisplayName() != "Coder" {
			t.Fatalf("created user = %v", in)
		}
		if s, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: in.GetToken()}); err != nil || !s.GetUser().GetEmailVerified() {
			t.Fatalf("the session works: %v, %v", s, err)
		}
		var hash string
		if err := e.system.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1::uuid`, in.GetUser().GetId()).Scan(&hash); err != nil || hash != "" {
			t.Errorf("a code account has no password: %q, %v", hash, err)
		}
		_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword})
		wantCode(t, "password sign-in of a code account", err, codes.Unauthenticated, "identity.auth.invalid_credentials")
		_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: ""})
		wantCode(t, "empty password", err, codes.InvalidArgument, "")
		// It can get a password later through "forgot password", which proves the same mailbox.
		if _, err := e.ident.RequestPasswordReset(ctx, &plansv1.RequestPasswordResetRequest{Email: addr}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ident.ResetPassword(ctx, &plansv1.ResetPasswordRequest{Token: e.mail.lastToken(usermail.KindReset, addr), NewPassword: newPassword}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: newPassword}); err != nil {
			t.Errorf("password sign-in after a reset: %v", err)
		}
	})

	t.Run("without an account name the organisation is named after the address", func(t *testing.T) {
		const addr = "otp-noname@example.com"
		e.requestCode(t, addr)
		in, err := e.ident.VerifyLoginCode(ctx, &plansv1.VerifyLoginCodeRequest{Email: addr, Code: e.mail.lastCode(addr)})
		if err != nil || in.GetMemberships()[0].GetAccountName() != "otp-noname" {
			t.Fatalf("%v, %v", in, err)
		}
	})

	t.Run("a code works once", func(t *testing.T) {
		const addr = "otp-once@example.com"
		e.requestCode(t, addr)
		code := e.mail.lastCode(addr)
		if _, err := e.verifyCode(addr, code); err != nil {
			t.Fatal(err)
		}
		_, err := e.verifyCode(addr, code)
		wantCode(t, "second use", err, codes.InvalidArgument, "identity.otp.invalid")
	})

	t.Run("an existing user signs in with a code and gets no second account", func(t *testing.T) {
		const addr = "otp-existing@example.com"
		first := e.signUp(t, addr, "Existing Co")
		e.verify(t, addr)
		e.requestCode(t, addr)
		in, err := e.verifyCode(addr, e.mail.lastCode(addr))
		if err != nil || in.GetUser().GetId() != first.GetUser().GetId() || len(in.GetMemberships()) != 1 || in.GetMemberships()[0].GetAccountName() != "Existing Co" {
			t.Fatalf("code sign-in of an existing user: %v, %v", in, err)
		}
		if m := e.mail.to(usermail.KindCode, addr)[0]; m.Locale != "en" {
			t.Errorf("an existing user's own language wins: %q", m.Locale)
		}
		// The password still works, and the earlier session is not touched (the address was verified).
		if _, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword}); err != nil {
			t.Errorf("password after a code sign-in: %v", err)
		}
		if _, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: first.GetToken()}); err != nil {
			t.Errorf("the earlier session of a verified user: %v", err)
		}
	})

	t.Run("a code for an address never verified claims it: the password dies and so do the old sessions", func(t *testing.T) {
		const addr = "otp-squatted@example.com"
		squatter := e.signUp(t, addr, "Squatter Co") // somebody registered an address that is not theirs
		e.requestCode(t, addr)
		in, err := e.verifyCode(addr, e.mail.lastCode(addr))
		if err != nil || !in.GetUser().GetEmailVerified() {
			t.Fatalf("the mailbox owner signs in: %v, %v", in, err)
		}
		_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword})
		wantCode(t, "the squatter's password", err, codes.Unauthenticated, "identity.auth.invalid_credentials")
		_, err = e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: squatter.GetToken()})
		wantCode(t, "the squatter's session", err, codes.Unauthenticated, "identity.session.invalid")
		if _, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: in.GetToken()}); err != nil {
			t.Errorf("the new session: %v", err)
		}
	})

	t.Run("five wrong tries burn the code, even for the right one after", func(t *testing.T) {
		const addr = "otp-burn@example.com"
		e.requestCode(t, addr)
		right := e.mail.lastCode(addr)
		wrong := "000000"
		if right == wrong {
			wrong = "000001"
		}
		for i := 1; i <= 5; i++ {
			_, err := e.verifyCode(addr, wrong)
			wantCode(t, "wrong try", err, codes.InvalidArgument, "identity.otp.invalid")
		}
		var attempts int
		if err := e.system.QueryRow(ctx, `SELECT attempts FROM login_codes WHERE email = $1 AND used_at IS NULL`, addr).Scan(&attempts); err != nil || attempts != 5 {
			t.Fatalf("attempts = %d, %v", attempts, err)
		}
		_, err := e.verifyCode(addr, right)
		wantCode(t, "the right code after five wrong tries", err, codes.InvalidArgument, "identity.otp.invalid")
		// A new code starts fresh.
		e.ageCodes(t, addr, "2 minutes")
		e.requestCode(t, addr)
		if _, err := e.verifyCode(addr, e.mail.lastCode(addr)); err != nil {
			t.Errorf("a fresh code after a burned one: %v", err)
		}
	})

	t.Run("four wrong tries leave the code usable", func(t *testing.T) {
		const addr = "otp-four@example.com"
		e.requestCode(t, addr)
		right := e.mail.lastCode(addr)
		wrong := "999999"
		if right == wrong {
			wrong = "999998"
		}
		for i := 0; i < 4; i++ {
			_, _ = e.verifyCode(addr, wrong)
		}
		if _, err := e.verifyCode(addr, right); err != nil {
			t.Errorf("fifth try with the right code: %v", err)
		}
	})

	t.Run("an expired code is refused", func(t *testing.T) {
		const addr = "otp-expired@example.com"
		e.requestCode(t, addr)
		code := e.mail.lastCode(addr)
		e.ageCodes(t, addr, "11 minutes")
		_, err := e.verifyCode(addr, code)
		wantCode(t, "expired", err, codes.InvalidArgument, "identity.otp.invalid")
	})

	t.Run("a new code replaces the older one", func(t *testing.T) {
		const addr = "otp-replace@example.com"
		e.requestCode(t, addr)
		old := e.mail.lastCode(addr)
		e.ageCodes(t, addr, "2 minutes")
		e.requestCode(t, addr)
		fresh := e.mail.lastCode(addr)
		if fresh == old {
			t.Skip("the two random codes collided (one in a million)")
		}
		_, err := e.verifyCode(addr, old)
		wantCode(t, "the replaced code", err, codes.InvalidArgument, "identity.otp.invalid")
		if _, err := e.verifyCode(addr, fresh); err != nil {
			t.Errorf("the new code: %v", err)
		}
	})

	t.Run("garbage is the same refusal as a wrong code", func(t *testing.T) {
		const addr = "otp-garbage@example.com"
		e.requestCode(t, addr)
		for name, c := range map[string]string{"empty": "", "short": "12345", "long": "1234567", "letters": "12345a", "spaces": "12 456", "sql": "1' OR '1"} {
			_, err := e.verifyCode(addr, c)
			wantCode(t, name, err, codes.InvalidArgument, "identity.otp.invalid")
		}
		_, err := e.verifyCode("not an address", "123456")
		wantCode(t, "a malformed address", err, codes.InvalidArgument, "identity.otp.invalid")
	})

	t.Run("a code for one address does not work for another", func(t *testing.T) {
		e.requestCode(t, "otp-a@example.com")
		e.requestCode(t, "otp-b@example.com")
		_, err := e.verifyCode("otp-b@example.com", e.mail.lastCode("otp-a@example.com"))
		if e.mail.lastCode("otp-a@example.com") != e.mail.lastCode("otp-b@example.com") {
			wantCode(t, "a's code on b", err, codes.InvalidArgument, "identity.otp.invalid")
		}
	})

	t.Run("no enumeration: a known and an unknown address are answered alike", func(t *testing.T) {
		known := "otp-known@example.com"
		e.signUp(t, known, "Known Co")
		unknown := "otp-unknown@example.com"
		for _, a := range []string{known, unknown} {
			if _, err := e.ident.RequestLoginCode(ctx, &plansv1.RequestLoginCodeRequest{Email: a}); err != nil {
				t.Fatalf("request for %s: %v", a, err)
			}
			if len(e.mail.to(usermail.KindCode, a)) != 1 {
				t.Errorf("a code is mailed for %s", a)
			}
		}
		_, errKnown := e.verifyCode(known, "000000")
		_, errUnknown := e.verifyCode("otp-never@example.com", "000000")
		if status.Code(errKnown) != status.Code(errUnknown) || status.Convert(errKnown).Message() != status.Convert(errUnknown).Message() {
			t.Errorf("answers differ: %v vs %v", errKnown, errUnknown)
		}
	})

	t.Run("a right code spent twice at once signs in once", func(t *testing.T) {
		const addr = "otp-race@example.com"
		e.requestCode(t, addr)
		code := e.mail.lastCode(addr)
		wins := 0
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.verifyCode(addr, code); err == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("%d requests signed in with one code, want exactly 1", wins)
		}
		var users int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = $1`, addr).Scan(&users); err != nil || users != 1 {
			t.Errorf("users = %d, %v", users, err)
		}
	})

	t.Run("parallel guesses cannot beat the attempt limit", func(t *testing.T) {
		const addr = "otp-guess@example.com"
		e.requestCode(t, addr)
		right := e.mail.lastCode(addr)
		wrong := "123123"
		if right == wrong {
			wrong = "321321"
		}
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, _ = e.verifyCode(addr, wrong) }()
		}
		wg.Wait()
		var attempts int
		if err := e.system.QueryRow(ctx, `SELECT attempts FROM login_codes WHERE email = $1 AND used_at IS NULL`, addr).Scan(&attempts); err != nil || attempts > 5 {
			t.Fatalf("attempts = %d (%v): the limit is 5 however many guesses arrive together", attempts, err)
		}
	})

	// ---- an identity provider ----

	identity := func(sub, email string, verified bool) *plansv1.SignInWithIdentityRequest {
		return &plansv1.SignInWithIdentityRequest{Provider: "google", Subject: sub, Email: email, EmailVerified: verified, DisplayName: "Gina G", Locale: "fr"}
	}

	t.Run("a new person is created from the provider's verified address", func(t *testing.T) {
		in, err := e.ident.SignInWithIdentity(ctx, identity("g-new", "Google-New@Example.com", true))
		if err != nil || !in.GetUser().GetEmailVerified() || in.GetUser().GetEmail() != "google-new@example.com" || len(in.GetMemberships()) != 1 || in.GetMemberships()[0].GetRole() != "owner" {
			t.Fatalf("%v, %v", in, err)
		}
		var n int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM user_identities WHERE provider = 'google' AND subject = 'g-new' AND user_id = $1::uuid`, in.GetUser().GetId()).Scan(&n); err != nil || n != 1 {
			t.Fatalf("identity rows = %d, %v", n, err)
		}
		// The next login finds the person by the subject, even when Google reports another address.
		again, err := e.ident.SignInWithIdentity(ctx, identity("g-new", "changed@example.com", true))
		if err != nil || again.GetUser().GetId() != in.GetUser().GetId() || len(again.GetMemberships()) != 1 {
			t.Fatalf("second login: %v, %v", again, err)
		}
		var users int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = 'changed@example.com'`).Scan(&users); err != nil || users != 0 {
			t.Errorf("a changed provider address created a user: %d, %v", users, err)
		}
	})

	t.Run("an address the provider has not verified is never used", func(t *testing.T) {
		_, err := e.ident.SignInWithIdentity(ctx, identity("g-unverified", "g-unverified@example.com", false))
		wantCode(t, "unverified provider address", err, codes.PermissionDenied, "identity.oauth.email_unverified")
		var n int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = 'g-unverified@example.com'`).Scan(&n); err != nil || n != 0 {
			t.Errorf("a user was created: %d, %v", n, err)
		}
		// Not for linking either: the account of that address stays untouched.
		victim := e.signUp(t, "g-victim@example.com", "Victim Co")
		_, err = e.ident.SignInWithIdentity(ctx, identity("g-evil", "g-victim@example.com", false))
		wantCode(t, "unverified address of an existing user", err, codes.PermissionDenied, "identity.oauth.email_unverified")
		if _, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: victim.GetToken()}); err != nil {
			t.Errorf("the victim's session: %v", err)
		}
		var links int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM user_identities WHERE subject = 'g-evil'`).Scan(&links); err != nil || links != 0 {
			t.Errorf("an identity was linked: %d, %v", links, err)
		}
	})

	t.Run("it links to the existing user with the same verified address and the password keeps working", func(t *testing.T) {
		const addr = "g-link@example.com"
		first := e.signUp(t, addr, "Link Co")
		e.verify(t, addr)
		in, err := e.ident.SignInWithIdentity(ctx, identity("g-link", addr, true))
		if err != nil || in.GetUser().GetId() != first.GetUser().GetId() || len(in.GetMemberships()) != 1 || in.GetMemberships()[0].GetAccountName() != "Link Co" {
			t.Fatalf("link: %v, %v", in, err)
		}
		if _, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword}); err != nil {
			t.Errorf("password after linking: %v", err)
		}
		if _, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: first.GetToken()}); err != nil {
			t.Errorf("the verified user's earlier session: %v", err)
		}
	})

	t.Run("linking to an address nobody verified clears the password and the old sessions", func(t *testing.T) {
		const addr = "g-squatted@example.com"
		squatter := e.signUp(t, addr, "Squatter Co")
		in, err := e.ident.SignInWithIdentity(ctx, identity("g-owner", addr, true))
		if err != nil || !in.GetUser().GetEmailVerified() || in.GetUser().GetId() != squatter.GetUser().GetId() {
			t.Fatalf("%v, %v", in, err)
		}
		_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword})
		wantCode(t, "the squatter's password", err, codes.Unauthenticated, "identity.auth.invalid_credentials")
		_, err = e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: squatter.GetToken()})
		wantCode(t, "the squatter's session", err, codes.Unauthenticated, "identity.session.invalid")
	})

	t.Run("the provider's subject is unique and bad providers are refused", func(t *testing.T) {
		in, err := e.ident.SignInWithIdentity(ctx, identity("g-unique", "g-unique@example.com", true))
		if err != nil {
			t.Fatal(err)
		}
		other := e.signUp(t, "g-unique-2@example.com", "Other Co")
		if _, err := e.system.Exec(ctx, `INSERT INTO user_identities (id, user_id, provider, subject, email) VALUES (gen_random_uuid(), $1::uuid, 'google', 'g-unique', 'x@example.com')`, other.GetUser().GetId()); err == nil {
			t.Error("two users share one provider subject")
		}
		if in.GetUser().GetId() == other.GetUser().GetId() {
			t.Error("sanity")
		}
		bad := identity("s", "g-bad@example.com", true)
		bad.Provider = "facebook"
		_, err = e.ident.SignInWithIdentity(ctx, bad)
		wantCode(t, "unknown provider", err, codes.InvalidArgument, "identity.oauth.invalid")
		bad = identity("", "g-bad@example.com", true)
		_, err = e.ident.SignInWithIdentity(ctx, bad)
		wantCode(t, "no subject", err, codes.InvalidArgument, "identity.oauth.invalid")
	})

	t.Run("a provider account has no password to guess", func(t *testing.T) {
		in, err := e.ident.SignInWithIdentity(ctx, identity("g-nopass", "g-nopass@example.com", true))
		if err != nil {
			t.Fatal(err)
		}
		for _, pw := range []string{goodPassword, "!", "$argon2id$", "x"} {
			_, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: in.GetUser().GetEmail(), Password: pw})
			if status.Code(err) != codes.Unauthenticated {
				t.Errorf("password %q: %v", pw, err)
			}
		}
	})
}
