//go:build integration

package itest

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
)

const goodPassword = "a long enough password"

// signUp signs up and then signs in (sign-up itself opens no session) and returns the session.
func (e *env) signUp(t *testing.T, email, account string) *plansv1.SignInResponse {
	t.Helper()
	if _, err := e.ident.SignUp(context.Background(), &plansv1.SignUpRequest{Email: email, Password: goodPassword, DisplayName: "Test", AccountName: account}); err != nil {
		t.Fatalf("SignUp %s: %v", email, err)
	}
	r, err := e.ident.SignIn(context.Background(), &plansv1.SignInRequest{Email: email, Password: goodPassword})
	if err != nil {
		t.Fatalf("SignIn %s: %v", email, err)
	}
	return r
}

// verify follows the mailed verification link of an address.
func (e *env) verify(t *testing.T, email string) {
	t.Helper()
	tok := e.mail.lastToken(usermail.KindVerify, email)
	if tok == "" {
		t.Fatalf("no verification mail was sent to %s", email)
	}
	if _, err := e.ident.VerifyEmail(context.Background(), &plansv1.VerifyEmailRequest{Token: tok}); err != nil {
		t.Fatalf("VerifyEmail %s: %v", email, err)
	}
}

func wantCode(t *testing.T, what string, err error, c codes.Code, key string) {
	t.Helper()
	if status.Code(err) != c || (key != "" && errkey.Of(err) != key) {
		t.Errorf("%s: got %v (key %q), want %v %q", what, err, errkey.Of(err), c, key)
	}
}

func testIdentity(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// Sign-up validation, then success, then the same address in another case.
	for name, req := range map[string]*plansv1.SignUpRequest{
		"bad email":      {Email: "not-an-email", Password: goodPassword, AccountName: "A"},
		"short password": {Email: "x@example.com", Password: "short", AccountName: "A"},
		"no account":     {Email: "x@example.com", Password: goodPassword},
	} {
		_, err := e.ident.SignUp(ctx, req)
		wantCode(t, name, err, codes.InvalidArgument, "")
	}
	_, err := e.ident.SignUp(ctx, &plansv1.SignUpRequest{Email: "Ada@Example.com", Password: goodPassword, DisplayName: "Test", AccountName: "Ada Labs"})
	if err != nil {
		t.Fatal(err)
	}
	ada, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "ada@example.com", Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	if ada.GetUser().GetEmail() != "ada@example.com" || len(ada.GetMemberships()) != 1 || ada.GetMemberships()[0].GetRole() != "owner" || ada.GetUser().GetEmailVerified() {
		t.Fatalf("sign-in after sign-up = %v", ada)
	}
	if !strings.HasPrefix(ada.GetToken(), "qs_") {
		t.Fatalf("token %q", ada.GetToken())
	}
	if len(e.mail.to(usermail.KindVerify, "ada@example.com")) != 1 {
		t.Fatalf("sign-up must mail one verification link, sent: %+v", e.mail.sent)
	}
	// The same address again (another case, another account name) answers exactly like a free one: no error, no second user, no session. The owner is
	// warned by mail instead, and only once within the hour.
	for i := 0; i < 2; i++ {
		resp, err := e.ident.SignUp(ctx, &plansv1.SignUpRequest{Email: "ADA@example.com", Password: goodPassword, AccountName: "Other"})
		if err != nil || resp.String() != "" {
			t.Fatalf("sign-up with a taken address must look like any other: %v, %v", resp, err)
		}
	}
	var users, accountsNamed int
	if err := e.system.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = 'ada@example.com'`).Scan(&users); err != nil || users != 1 {
		t.Fatalf("users with the address: %d, %v", users, err)
	}
	if err := e.system.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE name = 'Other'`).Scan(&accountsNamed); err != nil || accountsNamed != 0 {
		t.Fatalf("a taken address must not create an account: %d, %v", accountsNamed, err)
	}
	if got := len(e.mail.to(usermail.KindTaken, "ada@example.com")); got != 1 {
		t.Fatalf("the owner is warned once an hour, got %d warnings", got)
	}
	if len(e.mail.to(usermail.KindVerify, "ada@example.com")) != 1 {
		t.Fatal("a taken address gets no verification link")
	}
	if _, err := e.system.Exec(ctx, `UPDATE users SET taken_notice_at = now() - interval '2 hours' WHERE lower(email) = 'ada@example.com'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ident.SignUp(ctx, &plansv1.SignUpRequest{Email: "ada@example.com", Password: goodPassword, AccountName: "Other"}); err != nil {
		t.Fatal(err)
	}
	if got := len(e.mail.to(usermail.KindTaken, "ada@example.com")); got != 2 {
		t.Fatalf("a new warning after the hour: %d", got)
	}

	// The database holds the hash of the token and of the password, never the secrets.
	var stored []byte
	var pwHash string
	if err := e.system.QueryRow(ctx, `SELECT token_hash FROM user_sessions WHERE user_id = $1::uuid`, ada.GetUser().GetId()).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(ada.GetToken()))
	if string(stored) != string(want[:]) {
		t.Error("the stored token hash is not the SHA-256 of the token")
	}
	if err := e.system.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1::uuid`, ada.GetUser().GetId()).Scan(&pwHash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pwHash, "$argon2id$") || strings.Contains(pwHash, goodPassword) {
		t.Errorf("password hash = %q", pwHash)
	}

	// The session resolves; a made-up or malformed token does not.
	sess, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: ada.GetToken()})
	if err != nil || sess.GetUser().GetId() != ada.GetUser().GetId() {
		t.Fatalf("GetSession = %v, %v", sess, err)
	}
	for _, bad := range []string{"", "qs_nope", "qs_" + strings.Repeat("A", 43), "qk_test_abcdefgh_secret"} {
		_, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: bad})
		wantCode(t, "token "+bad, err, codes.Unauthenticated, "identity.session.invalid")
	}

	// Sliding: a session last used a while ago has its expiry moved on by the next use.
	if _, err := e.system.Exec(ctx, `UPDATE user_sessions SET last_seen_at = now() - interval '1 hour', expires_at = now() + interval '1 day' WHERE user_id = $1::uuid`, ada.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: ada.GetToken()}); err != nil {
		t.Fatal(err)
	}
	var expires time.Time
	if err := e.system.QueryRow(ctx, `SELECT expires_at FROM user_sessions WHERE user_id = $1::uuid`, ada.GetUser().GetId()).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if time.Until(expires) < 13*24*time.Hour {
		t.Errorf("expiry did not slide: %v", expires)
	}
	// An expired session is gone.
	if _, err := e.system.Exec(ctx, `UPDATE user_sessions SET expires_at = now() - interval '1 second' WHERE user_id = $1::uuid`, ada.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	_, err = e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: ada.GetToken()})
	wantCode(t, "expired session", err, codes.Unauthenticated, "identity.session.invalid")

	// Sign-in: one answer for a wrong password, an unknown address and a locked account.
	in, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: " ADA@example.com ", Password: goodPassword})
	if err != nil || in.GetToken() == ada.GetToken() || len(in.GetMemberships()) != 1 {
		t.Fatalf("SignIn = %v, %v", in, err)
	}
	_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "ada@example.com", Password: "wrong password!"})
	wantCode(t, "wrong password", err, codes.Unauthenticated, "identity.auth.invalid_credentials")
	_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "nobody@example.com", Password: "wrong password!"})
	wantCode(t, "unknown e-mail", err, codes.Unauthenticated, "identity.auth.invalid_credentials")
	for i := 0; i < 4; i++ { // five failures lock; one is already counted
		_, _ = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "ada@example.com", Password: "wrong password!"})
	}
	_, err = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "ada@example.com", Password: goodPassword})
	wantCode(t, "correct password while locked", err, codes.Unauthenticated, "identity.auth.invalid_credentials")
	// The lock runs out; the count starts over, so one slip does not lock again.
	if _, err := e.system.Exec(ctx, `UPDATE users SET locked_until = now() - interval '1 second' WHERE id = $1::uuid`, ada.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	_, _ = e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "ada@example.com", Password: "wrong password!"})
	if _, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "ada@example.com", Password: goodPassword}); err != nil {
		t.Errorf("sign-in after the lock ran out and one slip: %v", err)
	}

	// Sign-out revokes; signing out twice is fine.
	out, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: "ada@example.com", Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := e.ident.SignOut(ctx, &plansv1.SignOutRequest{Token: out.GetToken()}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: out.GetToken()})
	wantCode(t, "signed-out session", err, codes.Unauthenticated, "identity.session.invalid")

	t.Run("AppsAndKeys", func(t *testing.T) { testIdentityApps(t, e, in) })
}

func testIdentityApps(t *testing.T, e *env, ada *plansv1.SignInResponse) {
	ctx := context.Background()
	uid, acct := ada.GetUser().GetId(), ada.GetMemberships()[0].GetAccountId()
	bob := e.signUp(t, "bob@example.com", "Bob Inc")
	bid, bacct := bob.GetUser().GetId(), bob.GetMemberships()[0].GetAccountId()

	list, err := e.ident.ListMyApps(ctx, &plansv1.ListMyAppsRequest{UserId: uid, AccountId: acct})
	if err != nil || len(list.GetApps()) != 0 {
		t.Fatalf("a new account has no apps: %v, %v", list, err)
	}
	app, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: uid, AccountId: acct, Name: "Web shop", Currency: "USD", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	a := app.GetApp()
	if a.GetSlug() != "web-shop" || len(a.GetTenants()) != 2 {
		t.Fatalf("app = %v", a)
	}
	for _, tn := range a.GetTenants() {
		if tn.GetAccountId() != acct || tn.GetTier() != plansv1.TenantTier_TENANT_TIER_FREE {
			t.Errorf("tenant %v: not the account's free-tier tenant", tn)
		}
	}
	_, err = e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: uid, AccountId: acct, Name: "Web shop", Currency: "USD", Timezone: "UTC"})
	wantCode(t, "duplicate slug", err, codes.AlreadyExists, "plans.app.slug_taken")
	_, err = e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: uid, AccountId: acct, Name: "X", Currency: "XXX", Timezone: "UTC"})
	wantCode(t, "bad currency", err, codes.InvalidArgument, "")

	// The owner's view.
	got, err := e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: uid, AppId: a.GetId()})
	if err != nil || got.GetRole() != "owner" || len(got.GetApp().GetTenants()) != 2 {
		t.Fatalf("GetMyApp = %v, %v", got, err)
	}
	acc, err := e.ident.GetMyAccount(ctx, &plansv1.GetMyAccountRequest{UserId: uid, AccountId: acct})
	if err != nil || acc.GetName() != "Ada Labs" || len(acc.GetMembers()) != 1 || acc.GetMembers()[0].GetEmail() != "ada@example.com" {
		t.Fatalf("GetMyAccount = %v, %v", acc, err)
	}
	var live, sandbox *plansv1.Tenant
	for _, tn := range a.GetTenants() {
		if tn.GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE {
			live = tn
		} else {
			sandbox = tn
		}
	}
	if _, err := e.ident.ResolveMyTenant(ctx, &plansv1.ResolveMyTenantRequest{UserId: uid, TenantId: live.GetId()}); err != nil {
		t.Errorf("the owner resolves their tenant: %v", err)
	}

	// A live key needs a proven address; a sandbox key does not.
	_, err = e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: uid, AppId: a.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
	wantCode(t, "live key before the address is verified", err, codes.PermissionDenied, "identity.email.not_verified")
	if k, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: uid, AppId: a.GetId(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX}); err != nil {
		t.Fatalf("a sandbox key needs no verification: %v", err)
	} else if _, err := e.ident.RevokeMyApiKey(ctx, &plansv1.RevokeMyApiKeyRequest{UserId: uid, AppId: a.GetId(), ApiKeyId: k.GetApiKey().GetId()}); err != nil {
		t.Fatal(err)
	}
	e.verify(t, "ada@example.com")
	// A proven address is not enough: live is earned by the go-live checklist (phase 45), so the tenant has no keys until it is active.
	_, err = e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: uid, AppId: a.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
	wantCode(t, "live key of an inactive tenant", err, codes.FailedPrecondition, "plans.api_key.tenant_not_active")
	e.activate(t, live.GetId())

	// Keys: made per environment, shown once, listed without secrets, revoked and evicted.
	lk, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: uid, AppId: a.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
	if err != nil || !strings.HasPrefix(lk.GetPlaintext(), "qk_live_") || lk.GetApiKey().GetTenantId() != live.GetId() {
		t.Fatalf("live key = %v, %v", lk, err)
	}
	sk, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: uid, AppId: a.GetId(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX})
	if err != nil || !strings.HasPrefix(sk.GetPlaintext(), "qk_test_") || sk.GetApiKey().GetTenantId() != sandbox.GetId() {
		t.Fatalf("sandbox key = %v, %v", sk, err)
	}
	keys, err := e.ident.ListMyApiKeys(ctx, &plansv1.ListMyApiKeysRequest{UserId: uid, AppId: a.GetId()})
	if err != nil || len(keys.GetApiKeys()) != 3 {
		t.Fatalf("keys = %v, %v", keys, err)
	}
	if _, err := e.ident.RevokeMyApiKey(ctx, &plansv1.RevokeMyApiKeyRequest{UserId: uid, AppId: a.GetId(), ApiKeyId: lk.GetApiKey().GetId()}); err != nil {
		t.Fatal(err)
	}
	e.evicted.mu.Lock()
	evicted := append([]string(nil), e.evicted.prefixes...)
	e.evicted.mu.Unlock()
	found := false
	for _, p := range evicted {
		found = found || p == lk.GetApiKey().GetPrefix()
	}
	if !found {
		t.Error("a revoked key is evicted from the gateway cache")
	}

	// Bob is another account: he finds nothing of Ada's, whatever he asks, and Ada finds nothing of his.
	_, err = e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: bid, AppId: a.GetId()})
	wantCode(t, "other account's app", err, codes.NotFound, "identity.app.not_found")
	_, err = e.ident.ListMyApps(ctx, &plansv1.ListMyAppsRequest{UserId: bid, AccountId: acct})
	wantCode(t, "other account's app list", err, codes.NotFound, "identity.account.not_found")
	_, err = e.ident.GetMyAccount(ctx, &plansv1.GetMyAccountRequest{UserId: bid, AccountId: acct})
	wantCode(t, "other account", err, codes.NotFound, "identity.account.not_found")
	_, err = e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: bid, AccountId: acct, Name: "Sneaky", Currency: "USD", Timezone: "UTC"})
	wantCode(t, "create an app in another account", err, codes.NotFound, "identity.account.not_found")
	for _, tn := range []*plansv1.Tenant{live, sandbox} {
		_, err = e.ident.ResolveMyTenant(ctx, &plansv1.ResolveMyTenantRequest{UserId: bid, TenantId: tn.GetId()})
		wantCode(t, "other account's tenant", err, codes.NotFound, "identity.tenant.not_found")
	}
	_, err = e.ident.ListMyApiKeys(ctx, &plansv1.ListMyApiKeysRequest{UserId: bid, AppId: a.GetId()})
	wantCode(t, "other account's keys", err, codes.NotFound, "identity.app.not_found")
	_, err = e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: bid, AppId: a.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
	wantCode(t, "key for another account's app", err, codes.NotFound, "identity.app.not_found")
	_, err = e.ident.RevokeMyApiKey(ctx, &plansv1.RevokeMyApiKeyRequest{UserId: bid, AppId: a.GetId(), ApiKeyId: sk.GetApiKey().GetId()})
	wantCode(t, "revoke another account's key", err, codes.NotFound, "identity.app.not_found")
	// Bob's own app does not open a door to Ada's key either.
	bapp, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: bid, AccountId: bacct, Name: "Bob app", Currency: "EUR", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.ident.RevokeMyApiKey(ctx, &plansv1.RevokeMyApiKeyRequest{UserId: bid, AppId: bapp.GetApp().GetId(), ApiKeyId: sk.GetApiKey().GetId()})
	wantCode(t, "revoke Ada's key through Bob's app", err, codes.NotFound, "identity.api_key.not_found")
	if k, err := e.admin.ResolveApiKey(context.Background(), &plansv1.ResolveApiKeyRequest{Prefix: sk.GetApiKey().GetPrefix()}); err != nil || k.GetStatus() != plansv1.ApiKey_STATUS_ACTIVE {
		t.Errorf("Ada's sandbox key must still be active: %v, %v", k, err)
	}
	// Malformed ids find nothing, like unknown ones.
	_, err = e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: uid, AppId: "nope"})
	wantCode(t, "malformed app id", err, codes.NotFound, "identity.app.not_found")
	_, err = e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: uid, AppId: uuid.NewString()})
	wantCode(t, "unknown app id", err, codes.NotFound, "identity.app.not_found")

	// A viewer reads but changes nothing.
	view := e.signUp(t, "viewer@example.com", "Viewer Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'viewer')`, acct, view.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	vid := view.GetUser().GetId()
	if _, err := e.ident.GetMyApp(ctx, &plansv1.GetMyAppRequest{UserId: vid, AppId: a.GetId()}); err != nil {
		t.Errorf("a viewer reads the app: %v", err)
	}
	if _, err := e.ident.ListMyApiKeys(ctx, &plansv1.ListMyApiKeysRequest{UserId: vid, AppId: a.GetId()}); err != nil {
		t.Errorf("a viewer lists keys: %v", err)
	}
	_, err = e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: vid, AccountId: acct, Name: "Nope", Currency: "USD", Timezone: "UTC"})
	wantCode(t, "viewer creates an app", err, codes.PermissionDenied, "identity.account.forbidden")
	_, err = e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: vid, AppId: a.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
	wantCode(t, "viewer creates a key", err, codes.PermissionDenied, "identity.account.forbidden")
	_, err = e.ident.RevokeMyApiKey(ctx, &plansv1.RevokeMyApiKeyRequest{UserId: vid, AppId: a.GetId(), ApiKeyId: sk.GetApiKey().GetId()})
	wantCode(t, "viewer revokes a key", err, codes.PermissionDenied, "identity.account.forbidden")
	// Identity RPCs are not operator RPCs and the operator RPCs stay operator-only.
	_, err = e.admin.CreateApp(ctx, &plansv1.CreateAppRequest{AccountId: acct, Name: "X"})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("the admin API without the operator flag: %v", err)
	}
}
