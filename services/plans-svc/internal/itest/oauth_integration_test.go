//go:build integration

package itest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/oauth"
)

// The OAuth authorization server's grants, codes and tokens (plan 20, default, not agreed): consent cut to the role, a code that works once with the right PKCE verifier, a
// refresh token that works once, introspection that follows the person's membership, the credential for the API audience, revocation. Only hashes are stored.

const (
	oauthRes      = "https://mcp.example.com/mcp"
	oauthClientID = "https://client.example/meta.json"
	oauthCB       = "https://client.example/callback"
)

var oauthVerifier = strings.Repeat("v", 43)

func oauthChallenge(v string) string {
	s := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

func testOAuth(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.verified(t, "oauth-owner@example.com", "OAuth Co")
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: owner.id, AccountId: owner.account, Name: "Shop", Currency: "TRY", Timezone: "Europe/Istanbul"})
	if err != nil {
		t.Fatal(err)
	}
	var sandbox, live string
	for _, tn := range created.GetApp().GetTenants() {
		if tn.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX {
			sandbox = tn.GetId()
		} else {
			live = tn.GetId()
		}
	}
	viewer := e.verified(t, "oauth-viewer@example.com", "Viewer Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'viewer')`, owner.account, viewer.id); err != nil {
		t.Fatal(err)
	}

	grant := func(user, tenant string, scopes ...string) *plansv1.CreateOAuthGrantResponse {
		t.Helper()
		r, err := e.ident.CreateOAuthGrant(ctx, &plansv1.CreateOAuthGrantRequest{UserId: user, TenantId: tenant, ClientId: oauthClientID, ClientName: "Example Assistant", RedirectUri: oauthCB,
			CodeChallenge: oauthChallenge(oauthVerifier), Resource: oauthRes, Scopes: scopes})
		if err != nil {
			t.Fatalf("CreateOAuthGrant: %v", err)
		}
		return r
	}
	redeem := func(code string) (*plansv1.OAuthTokens, error) {
		return e.ident.ExchangeOAuthCode(ctx, &plansv1.ExchangeOAuthCodeRequest{Code: code, ClientId: oauthClientID, RedirectUri: oauthCB, CodeVerifier: oauthVerifier, Resource: oauthRes})
	}
	introspect := func(token string) *plansv1.OAuthTokenInfo {
		t.Helper()
		r, err := e.ident.IntrospectOAuthToken(ctx, &plansv1.IntrospectOAuthTokenRequest{Token: token, Resource: oauthRes})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	tokens := func(user, tenant string, scopes ...string) *plansv1.OAuthTokens {
		t.Helper()
		tk, err := redeem(grant(user, tenant, scopes...).GetCode())
		if err != nil {
			t.Fatalf("redeem: %v", err)
		}
		return tk
	}

	t.Run("consent is cut to the role, and live needs the switch and a verified address", func(t *testing.T) {
		both := []string{"customers:read", "customers:write", "events:write"}
		if got := grant(owner.id, sandbox, both...).GetScopes(); !slices.Equal(got, both) {
			t.Errorf("an owner got %v", got)
		}
		if got := grant(viewer.id, sandbox, both...).GetScopes(); !slices.Equal(got, []string{"customers:read"}) {
			t.Errorf("a viewer got %v, want the read scope only", got)
		}
		_, err := e.ident.CreateOAuthGrant(ctx, &plansv1.CreateOAuthGrantRequest{UserId: viewer.id, TenantId: sandbox, ClientId: oauthClientID, ClientName: "x", RedirectUri: oauthCB,
			CodeChallenge: oauthChallenge(oauthVerifier), Resource: oauthRes, Scopes: []string{"customers:write"}})
		wantCode(t, "a viewer asking only for a write scope", err, codes.InvalidArgument, "identity.oauth.bad_request")
		// Somebody else's tenant is not found, whatever the id.
		_, err = e.ident.CreateOAuthGrant(ctx, &plansv1.CreateOAuthGrantRequest{UserId: owner.id, TenantId: "00000000-0000-4000-8000-000000000000", ClientId: oauthClientID, ClientName: "x",
			RedirectUri: oauthCB, CodeChallenge: oauthChallenge(oauthVerifier), Resource: oauthRes, Scopes: []string{"customers:read"}})
		wantCode(t, "an unknown tenant", err, codes.NotFound, "identity.tenant.not_found")
		stranger := e.verified(t, "oauth-stranger@example.com", "Stranger Co")
		_, err = e.ident.CreateOAuthGrant(ctx, &plansv1.CreateOAuthGrantRequest{UserId: stranger.id, TenantId: sandbox, ClientId: oauthClientID, ClientName: "x", RedirectUri: oauthCB,
			CodeChallenge: oauthChallenge(oauthVerifier), Resource: oauthRes, Scopes: []string{"customers:read"}})
		wantCode(t, "another account's tenant", err, codes.NotFound, "identity.tenant.not_found")
		// Live: refused unless the deployment allows it (and then the address must be verified).
		_, err = e.ident.CreateOAuthGrant(ctx, &plansv1.CreateOAuthGrantRequest{UserId: owner.id, TenantId: live, ClientId: oauthClientID, ClientName: "x", RedirectUri: oauthCB,
			CodeChallenge: oauthChallenge(oauthVerifier), Resource: oauthRes, Scopes: []string{"customers:read"}})
		wantCode(t, "live while off", err, codes.PermissionDenied, "identity.oauth.live_disabled")
		_, err = e.ident.CreateOAuthGrant(ctx, &plansv1.CreateOAuthGrantRequest{UserId: owner.id, TenantId: live, ClientId: oauthClientID, ClientName: "x", RedirectUri: oauthCB,
			CodeChallenge: oauthChallenge(oauthVerifier), Resource: oauthRes, Scopes: []string{"customers:read"}, AllowLive: true})
		if err != nil {
			t.Errorf("live while on, verified: %v", err)
		}
		if _, err := e.system.Exec(ctx, `UPDATE users SET email_verified_at = NULL WHERE id = $1::uuid`, owner.id); err != nil {
			t.Fatal(err)
		}
		_, err = e.ident.CreateOAuthGrant(ctx, &plansv1.CreateOAuthGrantRequest{UserId: owner.id, TenantId: live, ClientId: oauthClientID, ClientName: "x", RedirectUri: oauthCB,
			CodeChallenge: oauthChallenge(oauthVerifier), Resource: oauthRes, Scopes: []string{"customers:read"}, AllowLive: true})
		wantCode(t, "live with an unverified address", err, codes.PermissionDenied, "identity.oauth.live_email_unverified")
		if _, err := e.system.Exec(ctx, `UPDATE users SET email_verified_at = now() WHERE id = $1::uuid`, owner.id); err != nil {
			t.Fatal(err)
		}
		// Incomplete or insecure requests: no PKCE challenge, a redirect that is not https or loopback, a bad resource.
		for name, mut := range map[string]func(*plansv1.CreateOAuthGrantRequest){
			"no challenge":        func(r *plansv1.CreateOAuthGrantRequest) { r.CodeChallenge = "" },
			"a short challenge":   func(r *plansv1.CreateOAuthGrantRequest) { r.CodeChallenge = "abc" },
			"a plain http client": func(r *plansv1.CreateOAuthGrantRequest) { r.RedirectUri = "http://client.example/cb" },
			"a bad resource":      func(r *plansv1.CreateOAuthGrantRequest) { r.Resource = "mcp.example.com" },
		} {
			req := &plansv1.CreateOAuthGrantRequest{UserId: owner.id, TenantId: sandbox, ClientId: oauthClientID, ClientName: "x", RedirectUri: oauthCB, CodeChallenge: oauthChallenge(oauthVerifier), Resource: oauthRes,
				Scopes: []string{"customers:read"}}
			mut(req)
			_, err := e.ident.CreateOAuthGrant(ctx, req)
			wantCode(t, name, err, codes.InvalidArgument, "identity.oauth.bad_request")
		}
		var n int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'oauth_grant.created' AND tenant_id = $1::uuid`, sandbox).Scan(&n); err != nil || n == 0 {
			t.Errorf("the consent was not recorded in the audit log (%d, %v)", n, err)
		}
	})

	t.Run("a code works once, with its own client, redirect, resource and verifier", func(t *testing.T) {
		for name, req := range map[string]*plansv1.ExchangeOAuthCodeRequest{
			"another verifier": {ClientId: oauthClientID, RedirectUri: oauthCB, CodeVerifier: strings.Repeat("w", 43), Resource: oauthRes},
			"another client":   {ClientId: "https://evil.example/m.json", RedirectUri: oauthCB, CodeVerifier: oauthVerifier, Resource: oauthRes},
			"another redirect": {ClientId: oauthClientID, RedirectUri: "https://client.example/other", CodeVerifier: oauthVerifier, Resource: oauthRes},
			"another resource": {ClientId: oauthClientID, RedirectUri: oauthCB, CodeVerifier: oauthVerifier, Resource: "https://other.example/mcp"},
			"no verifier":      {ClientId: oauthClientID, RedirectUri: oauthCB, Resource: oauthRes},
		} {
			req.Code = grant(owner.id, sandbox, "customers:read").GetCode()
			_, err := e.ident.ExchangeOAuthCode(ctx, req)
			wantCode(t, name, err, codes.InvalidArgument, "identity.oauth.invalid_grant")
			// A wrong try spends the code: the right verifier does not redeem it afterwards.
			_, err = redeem(req.Code)
			wantCode(t, name+", then the right request", err, codes.InvalidArgument, "identity.oauth.invalid_grant")
		}
		_, err := redeem("not-a-code")
		wantCode(t, "garbage", err, codes.InvalidArgument, "identity.oauth.invalid_grant")

		code := grant(owner.id, sandbox, "customers:read").GetCode()
		tk, err := redeem(code)
		if err != nil || !strings.HasPrefix(tk.GetAccessToken(), oauth.AccessPrefix) || !strings.HasPrefix(tk.GetRefreshToken(), oauth.RefreshPrefix) || tk.GetExpiresIn().AsDuration() != time.Hour {
			t.Fatalf("tokens = %v, %v", tk, err)
		}
		if !introspect(tk.GetAccessToken()).GetActive() {
			t.Fatal("the new access token is not active")
		}
		// Used a second time: refused, and the grant it opened ends (both tokens stop).
		_, err = redeem(code)
		wantCode(t, "a code used twice", err, codes.InvalidArgument, "identity.oauth.invalid_grant")
		if introspect(tk.GetAccessToken()).GetActive() {
			t.Error("the access token of a replayed code still works")
		}
		if _, err := e.ident.RefreshOAuthToken(ctx, &plansv1.RefreshOAuthTokenRequest{RefreshToken: tk.GetRefreshToken(), ClientId: oauthClientID, Resource: oauthRes}); err == nil {
			t.Error("the refresh token of a replayed code still works")
		}
		// An expired code.
		expired := grant(owner.id, sandbox, "customers:read").GetCode()
		if _, err := e.system.Exec(ctx, `UPDATE oauth_codes SET expires_at = now() - interval '1 second' WHERE code_hash = $1`, oauth.Hash(expired)); err != nil {
			t.Fatal(err)
		}
		_, err = redeem(expired)
		wantCode(t, "an expired code", err, codes.InvalidArgument, "identity.oauth.invalid_grant")
	})

	t.Run("introspection says who, where, what and until when, and only for the resource the token was issued for", func(t *testing.T) {
		tk := tokens(owner.id, sandbox, "customers:read", "invoices:read")
		info := introspect(tk.GetAccessToken())
		if !info.GetActive() || info.GetUserId() != owner.id || info.GetTenant().GetId() != sandbox || info.GetMemberRole() != "owner" || info.GetClientId() != oauthClientID ||
			info.GetClientName() != "Example Assistant" || info.GetResource() != oauthRes || !slices.Equal(info.GetScopes(), []string{"customers:read", "invoices:read"}) {
			t.Fatalf("introspect = %v", info)
		}
		if d := time.Until(info.GetExpireTime().AsTime()); d < 59*time.Minute || d > time.Hour {
			t.Errorf("expires in %v", d)
		}
		other, err := e.ident.IntrospectOAuthToken(ctx, &plansv1.IntrospectOAuthTokenRequest{Token: tk.GetAccessToken(), Resource: "https://other.example/mcp"})
		if err != nil || other.GetActive() {
			t.Errorf("another resource: %v, %v", other, err)
		}
		if introspect(tk.GetRefreshToken()).GetActive() {
			t.Error("a refresh token introspected as an access token")
		}
		if introspect("qo_"+strings.Repeat("A", 43)).GetActive() || introspect("garbage").GetActive() {
			t.Error("an unknown token was active")
		}
		// Expiry is the database's clock.
		if _, err := e.system.Exec(ctx, `UPDATE oauth_tokens SET expires_at = now() - interval '1 second' WHERE token_hash = $1`, oauth.Hash(tk.GetAccessToken())); err != nil {
			t.Fatal(err)
		}
		if introspect(tk.GetAccessToken()).GetActive() {
			t.Error("an expired access token is active")
		}
	})

	t.Run("the person's role and membership are looked at every time", func(t *testing.T) {
		tk := tokens(owner.id, sandbox, "customers:read", "customers:write")
		if got := introspect(tk.GetAccessToken()).GetScopes(); len(got) != 2 {
			t.Fatalf("scopes = %v", got)
		}
		if _, err := e.system.Exec(ctx, `UPDATE account_members SET role = 'viewer' WHERE account_id = $1::uuid AND user_id = $2::uuid`, owner.account, owner.id); err != nil {
			t.Fatal(err)
		}
		if got := introspect(tk.GetAccessToken()).GetScopes(); !slices.Equal(got, []string{"customers:read"}) {
			t.Errorf("a person made a viewer keeps %v", got)
		}
		if _, err := e.system.Exec(ctx, `DELETE FROM account_members WHERE account_id = $1::uuid AND user_id = $2::uuid`, owner.account, owner.id); err != nil {
			t.Fatal(err)
		}
		if introspect(tk.GetAccessToken()).GetActive() {
			t.Error("a person who left the account still has an active token")
		}
		if _, err := e.ident.RefreshOAuthToken(ctx, &plansv1.RefreshOAuthTokenRequest{RefreshToken: tk.GetRefreshToken(), ClientId: oauthClientID, Resource: oauthRes}); err == nil {
			t.Error("a person who left the account refreshed a token")
		}
		if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'owner')`, owner.account, owner.id); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a refresh token works once; used again it ends the grant", func(t *testing.T) {
		first := tokens(owner.id, sandbox, "customers:read")
		refreshReq := func(rt, client string) (*plansv1.OAuthTokens, error) {
			return e.ident.RefreshOAuthToken(ctx, &plansv1.RefreshOAuthTokenRequest{RefreshToken: rt, ClientId: client, Resource: oauthRes})
		}
		_, err := refreshReq(first.GetRefreshToken(), "https://evil.example/m.json")
		wantCode(t, "another client", err, codes.InvalidArgument, "identity.oauth.invalid_grant")
		// A refresh by the wrong client spent the token (it is a try like any other): the grant is the same, so use a fresh one for the real thing.
		first = tokens(owner.id, sandbox, "customers:read")
		second, err := refreshReq(first.GetRefreshToken(), oauthClientID)
		if err != nil || second.GetRefreshToken() == first.GetRefreshToken() || second.GetAccessToken() == first.GetAccessToken() {
			t.Fatalf("refresh = %v, %v: expected a new pair", second, err)
		}
		if !introspect(second.GetAccessToken()).GetActive() || !introspect(first.GetAccessToken()).GetActive() {
			t.Error("the access tokens should both live until they expire after a refresh")
		}
		_, err = refreshReq(first.GetRefreshToken(), oauthClientID)
		wantCode(t, "the old refresh token again", err, codes.InvalidArgument, "identity.oauth.invalid_grant")
		if introspect(second.GetAccessToken()).GetActive() {
			t.Error("a reused refresh token should end the whole grant")
		}
		if _, err := refreshReq(second.GetRefreshToken(), oauthClientID); err == nil {
			t.Error("the newer refresh token survived the reuse")
		}
		// An expired refresh token.
		third := tokens(owner.id, sandbox, "customers:read")
		if _, err := e.system.Exec(ctx, `UPDATE oauth_tokens SET expires_at = now() - interval '1 second' WHERE token_hash = $1`, oauth.Hash(third.GetRefreshToken())); err != nil {
			t.Fatal(err)
		}
		_, err = refreshReq(third.GetRefreshToken(), oauthClientID)
		wantCode(t, "an expired refresh token", err, codes.InvalidArgument, "identity.oauth.invalid_grant")
	})

	t.Run("the credential for the API audience lives minutes, is a token of its own and ends with the grant", func(t *testing.T) {
		tk := tokens(owner.id, sandbox, "customers:read")
		m, err := e.ident.MintOAuthApiToken(ctx, &plansv1.MintOAuthApiTokenRequest{AccessToken: tk.GetAccessToken(), Resource: oauthRes})
		if err != nil || !strings.HasPrefix(m.GetToken(), oauth.APIPrefix) {
			t.Fatalf("mint = %v, %v", m, err)
		}
		if d := time.Until(m.GetExpireTime().AsTime()); d <= 0 || d > oauth.APITTL {
			t.Errorf("the credential lasts %v", d)
		}
		r, err := e.ident.ResolveOAuthApiToken(ctx, &plansv1.ResolveOAuthApiTokenRequest{Token: m.GetToken()})
		if err != nil || !r.GetActive() || r.GetTenant().GetId() != sandbox || r.GetUserId() != owner.id || !slices.Equal(r.GetScopes(), []string{"customers:read"}) || r.GetClientName() != "Example Assistant" {
			t.Fatalf("resolve = %v, %v", r, err)
		}
		// The kinds do not mix: an access token is not the API credential, nor the credential an access token.
		if r, _ := e.ident.ResolveOAuthApiToken(ctx, &plansv1.ResolveOAuthApiTokenRequest{Token: tk.GetAccessToken()}); r.GetActive() {
			t.Error("an access token was accepted as the API credential")
		}
		if introspect(m.GetToken()).GetActive() {
			t.Error("the API credential introspected as an access token")
		}
		// Minted only for the resource the access token was issued for, and not from a refresh token.
		_, err = e.ident.MintOAuthApiToken(ctx, &plansv1.MintOAuthApiTokenRequest{AccessToken: tk.GetAccessToken(), Resource: "https://other.example/mcp"})
		wantCode(t, "another resource", err, codes.Unauthenticated, "identity.oauth.invalid_token")
		_, err = e.ident.MintOAuthApiToken(ctx, &plansv1.MintOAuthApiTokenRequest{AccessToken: tk.GetRefreshToken(), Resource: oauthRes})
		wantCode(t, "a refresh token", err, codes.Unauthenticated, "identity.oauth.invalid_token")
		// It never outlives the access token it came from.
		if _, err := e.system.Exec(ctx, `UPDATE oauth_tokens SET expires_at = now() + interval '30 seconds' WHERE token_hash = $1`, oauth.Hash(tk.GetAccessToken())); err != nil {
			t.Fatal(err)
		}
		short, err := e.ident.MintOAuthApiToken(ctx, &plansv1.MintOAuthApiTokenRequest{AccessToken: tk.GetAccessToken(), Resource: oauthRes})
		if err != nil || time.Until(short.GetExpireTime().AsTime()) > 31*time.Second {
			t.Errorf("a credential from a token with 30 s left lasts %v (%v)", time.Until(short.GetExpireTime().AsTime()), err)
		}
		// Revoking the grant stops it.
		if _, err := e.ident.RevokeOAuthToken(ctx, &plansv1.RevokeOAuthTokenRequest{Token: tk.GetAccessToken(), ClientId: oauthClientID}); err != nil {
			t.Fatal(err)
		}
		if r, _ := e.ident.ResolveOAuthApiToken(ctx, &plansv1.ResolveOAuthApiTokenRequest{Token: m.GetToken()}); r.GetActive() {
			t.Error("the API credential survived the revocation of its grant")
		}
	})

	t.Run("revocation by the client, and by the person from the console", func(t *testing.T) {
		tk := tokens(owner.id, sandbox, "customers:read")
		// Another client revokes nothing, and is not told.
		if _, err := e.ident.RevokeOAuthToken(ctx, &plansv1.RevokeOAuthTokenRequest{Token: tk.GetAccessToken(), ClientId: "https://evil.example/m.json"}); err != nil {
			t.Fatal(err)
		}
		if !introspect(tk.GetAccessToken()).GetActive() {
			t.Fatal("another client revoked the token")
		}
		if _, err := e.ident.RevokeOAuthToken(ctx, &plansv1.RevokeOAuthTokenRequest{Token: "garbage", ClientId: oauthClientID}); err != nil {
			t.Errorf("an unknown token: %v", err)
		}
		// The refresh token ends the grant too.
		if _, err := e.ident.RevokeOAuthToken(ctx, &plansv1.RevokeOAuthTokenRequest{Token: tk.GetRefreshToken(), ClientId: oauthClientID}); err != nil {
			t.Fatal(err)
		}
		if introspect(tk.GetAccessToken()).GetActive() {
			t.Error("revoking the refresh token left the access token alive")
		}

		again := tokens(owner.id, sandbox, "customers:read")
		list, err := e.ident.ListMyOAuthGrants(ctx, &plansv1.ListMyOAuthGrantsRequest{UserId: owner.id})
		if err != nil || len(list.GetGrants()) == 0 {
			t.Fatalf("grants = %v, %v", list, err)
		}
		id := introspect(again.GetAccessToken()).GetGrantId()
		var found *plansv1.OAuthGrant
		for _, g := range list.GetGrants() {
			if g.GetId() == id {
				found = g
			}
			if g.GetTenantId() == "" || g.GetClientName() == "" || g.GetAppName() != "Shop" {
				t.Errorf("incomplete grant %v", g)
			}
		}
		if found == nil || found.GetEnvironment() != plansv1.Environment_ENVIRONMENT_SANDBOX {
			t.Fatalf("grant %s is not listed: %v", id, list.GetGrants())
		}
		// Somebody else's grant is not found; their own is revoked and leaves the list.
		_, err = e.ident.RevokeMyOAuthGrant(ctx, &plansv1.RevokeMyOAuthGrantRequest{UserId: viewer.id, GrantId: id})
		wantCode(t, "another person's grant", err, codes.NotFound, "identity.oauth.grant_not_found")
		if !introspect(again.GetAccessToken()).GetActive() {
			t.Fatal("another person revoked the grant")
		}
		if _, err := e.ident.RevokeMyOAuthGrant(ctx, &plansv1.RevokeMyOAuthGrantRequest{UserId: owner.id, GrantId: id}); err != nil {
			t.Fatal(err)
		}
		if introspect(again.GetAccessToken()).GetActive() {
			t.Error("the grant was revoked and the token still works")
		}
		_, err = e.ident.RevokeMyOAuthGrant(ctx, &plansv1.RevokeMyOAuthGrantRequest{UserId: owner.id, GrantId: id})
		wantCode(t, "the same grant twice", err, codes.NotFound, "identity.oauth.grant_not_found")
		after, _ := e.ident.ListMyOAuthGrants(ctx, &plansv1.ListMyOAuthGrantsRequest{UserId: owner.id})
		for _, g := range after.GetGrants() {
			if g.GetId() == id {
				t.Error("a revoked grant is still listed")
			}
		}
		var n int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'oauth_grant.revoked' AND tenant_id = $1::uuid`, sandbox).Scan(&n); err != nil || n == 0 {
			t.Errorf("the revocation was not recorded in the audit log (%d, %v)", n, err)
		}
	})

	t.Run("only hashes are stored", func(t *testing.T) {
		g := grant(owner.id, sandbox, "customers:read")
		tk, err := redeem(g.GetCode())
		if err != nil {
			t.Fatal(err)
		}
		for name, plain := range map[string]string{"the code": g.GetCode(), "the access token": tk.GetAccessToken(), "the refresh token": tk.GetRefreshToken()} {
			table, col := "oauth_tokens", "token_hash"
			if name == "the code" {
				table, col = "oauth_codes", "code_hash"
			}
			var stored []byte
			if err := e.system.QueryRow(ctx, `SELECT `+col+` FROM `+table+` WHERE `+col+` = $1`, oauth.Hash(plain)).Scan(&stored); err != nil || !bytes.Equal(stored, oauth.Hash(plain)) {
				t.Errorf("%s: no row under its hash (%v)", name, err)
			}
			var n int
			if err := e.system.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE encode(`+col+`, 'escape') LIKE '%' || $1 || '%'`, plain).Scan(&n); err != nil || n != 0 {
				t.Errorf("%s is stored in the clear (%d, %v)", name, n, err)
			}
		}
	})
}
