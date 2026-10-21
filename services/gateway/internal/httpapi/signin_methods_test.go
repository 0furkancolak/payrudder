package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/oidctest"
	"github.com/0furkancolak/quotify/services/gateway/internal/oidc"
)

// Phase 42: the three ways to sign in and the operator who is a person.

func (f *fakeIdentity) RequestLoginCode(_ context.Context, req *plansv1.RequestLoginCodeRequest) (*plansv1.RequestLoginCodeResponse, error) {
	f.mu.Lock()
	f.codeRequests = append(f.codeRequests, req)
	f.mu.Unlock()
	if !strings.Contains(req.GetEmail(), "@") {
		return nil, errkey.New(codes.InvalidArgument, "common.invalid_argument", "bad address")
	}
	return &plansv1.RequestLoginCodeResponse{}, nil
}

func (f *fakeIdentity) VerifyLoginCode(_ context.Context, req *plansv1.VerifyLoginCodeRequest) (*plansv1.SignInResponse, error) {
	f.mu.Lock()
	f.codeVerifies = append(f.codeVerifies, req)
	f.mu.Unlock()
	if req.GetCode() != "123456" {
		return nil, errkey.New(codes.InvalidArgument, "identity.otp.invalid", "wrong code")
	}
	verified := true
	return &plansv1.SignInResponse{User: &plansv1.User{Id: opUser, Email: req.GetEmail(), EmailVerified: verified}, Token: tokOperator, ExpireTime: timestamppb.Now()}, nil
}

func (f *fakeIdentity) SignInWithIdentity(_ context.Context, req *plansv1.SignInWithIdentityRequest) (*plansv1.SignInResponse, error) {
	f.mu.Lock()
	f.identities = append(f.identities, req)
	f.mu.Unlock()
	return &plansv1.SignInResponse{User: &plansv1.User{Id: adaUser, Email: req.GetEmail(), EmailVerified: true}, Token: tokAda, ExpireTime: timestamppb.New(time.Now().Add(time.Hour))}, nil
}

func withOperators(emails ...string) func(*Deps) {
	return func(d *Deps) { d.OperatorEmails = NormalizeOperatorEmails(emails) }
}

func TestOperatorIsAPersonOnTheList(t *testing.T) {
	p := newProduct(t, nil, withOperators("Boss@Example.com", "  "))
	// The listed, verified person reaches operator routes with a session, as the machine token does.
	if resp, m := p.call(t, "POST", "/admin/v1/accounts", tokOperator, "", `{"name":"Acme"}`); resp.StatusCode != 201 || m["name"] != "Acme" {
		t.Fatalf("operator session: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", "/admin/v1/accounts", operatorToken, "", `{"name":"Acme"}`); resp.StatusCode != 201 {
		t.Fatalf("the machine token must keep working: %d", resp.StatusCode)
	}
	// Everybody else does not: a plain user, the right address never verified, an address that only looks like it, no session, a rubbish session.
	for name, tok := range map[string]string{"plain user": tokAda, "unverified address": tokOperatorUnverified, "lookalike address": tokSpoof, "none": "", "unknown session": "qs_nobody"} {
		resp, m := p.call(t, "POST", "/admin/v1/accounts", tok, "", `{"name":"Acme"}`)
		want := 403
		if tok == "" || tok == "qs_nobody" {
			want = 401
		}
		if resp.StatusCode != want {
			t.Errorf("%s: %d %v, want %d", name, resp.StatusCode, m, want)
		}
	}
}

func TestOperatorListIsEmptyByDefault(t *testing.T) {
	p := newProduct(t, nil, nil)
	if resp, _ := p.call(t, "POST", "/admin/v1/accounts", tokOperator, "", `{"name":"Acme"}`); resp.StatusCode != 403 {
		t.Fatalf("with no operator list nobody is an operator: %d", resp.StatusCode)
	}
}

func TestSpoofedOperatorClaimsAreIgnored(t *testing.T) {
	p := newProduct(t, nil, withOperators("boss@example.com"))
	req, _ := http.NewRequest("POST", p.ts.URL+"/admin/v1/accounts", strings.NewReader(`{"name":"Acme","operator":true,"is_operator":true,"email":"boss@example.com"}`))
	req.Header.Set("Authorization", "Bearer "+tokAda)
	for _, h := range []string{"X-Quotify-Operator", "X-Operator", "X-Forwarded-User", "X-Quotify-Role", "X-Quotify-Actor"} {
		req.Header.Set(h, "true")
	}
	req.Header.Set("X-Quotify-Tenant", adaLive)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("a client claim made Ada an operator: %d", resp.StatusCode)
	}
	// Sign-in bodies cannot claim it either, and the answer carries the server's verdict.
	_, m := p.call(t, "POST", "/auth/signin", "", "", `{"email":"boss@example.com","password":"right password!","operator":true}`)
	if m["operator"] != false {
		t.Errorf("the sign-in answer must say operator=false for Ada: %v", m)
	}
}

func TestMeSaysWhetherThePersonIsAnOperator(t *testing.T) {
	p := newProduct(t, nil, withOperators("boss@example.com"))
	for tok, want := range map[string]bool{tokOperator: true, tokAda: false, tokOperatorUnverified: false} {
		resp, m := p.call(t, "GET", "/auth/me", tok, "", "")
		if resp.StatusCode != 200 || m["operator"] != want {
			t.Errorf("%s: %d operator=%v, want %v", tok, resp.StatusCode, m["operator"], want)
		}
	}
}

func TestOperatorSessionActsAsAnyTenant(t *testing.T) {
	p := newProduct(t, nil, withOperators("boss@example.com"))
	body := `{"external_id":"cus_1","name":"Ada","email":"a@x.io"}`
	// Bob's tenant is not the operator's own, and the operator may act as it; Ada (not an operator) may not.
	if resp, m := p.call(t, "POST", "/v1/customers", tokOperator, bobLive, body); resp.StatusCode != 201 {
		t.Fatalf("operator acting as a tenant: %d %v", resp.StatusCode, m)
	}
	if got := p.plans.tenants[len(p.plans.tenants)-1]; got != bobLive {
		t.Errorf("plans-svc saw tenant %s", got)
	}
	if resp, _ := p.call(t, "POST", "/v1/customers", tokAda, bobLive, body); resp.StatusCode != 404 {
		t.Errorf("a plain user acting as another's tenant: %d", resp.StatusCode)
	}
	// An unverified holder of the listed address gets no such power.
	if resp, _ := p.call(t, "POST", "/v1/customers", tokOperatorUnverified, bobLive, body); resp.StatusCode == 201 {
		t.Errorf("unverified operator address acted as a tenant")
	}
}

func TestOneTimeCodeRoutes(t *testing.T) {
	p := newProduct(t, nil, withOperators("boss@example.com"))
	resp, m := p.call(t, "POST", "/auth/otp/request", "", "", `{"email":"x@example.com","locale":"tr"}`)
	if resp.StatusCode != 202 || m["status"] != "accepted" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("request: %d %v", resp.StatusCode, m)
	}
	if got := p.ident.codeRequests[0]; got.GetEmail() != "x@example.com" || got.GetLocale() != "tr" {
		t.Errorf("passed on: %v", got)
	}
	// A wrong code is a keyed 400; the right one opens a session and carries the operator verdict.
	resp, m = p.call(t, "POST", "/auth/otp/verify", "", "", `{"email":"boss@example.com","code":"000000"}`)
	if resp.StatusCode != 400 || errKeyOf(m) != "identity.otp.invalid" {
		t.Fatalf("wrong code: %d %v", resp.StatusCode, m)
	}
	resp, m = p.call(t, "POST", "/auth/otp/verify", "", "", `{"email":"boss@example.com","code":"123456","account_name":"Boss Co","user_id":"x","operator":false}`)
	if resp.StatusCode != 200 || m["token"] != tokOperator || m["operator"] != true || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("right code: %d %v", resp.StatusCode, m)
	}
	if got := p.ident.codeVerifies[len(p.ident.codeVerifies)-1]; got.GetAccountName() != "Boss Co" {
		t.Errorf("account name not passed on: %v", got)
	}
}

func TestOneTimeCodeRoutesAreRateLimitedPerAddress(t *testing.T) {
	p := newProduct(t, nil, nil)
	last := 0
	for i := 0; i < otpRequestsPerMinute+2; i++ {
		resp, _ := p.call(t, "POST", "/auth/otp/request", "", "", `{"email":"x@example.com"}`)
		last = resp.StatusCode
	}
	if last != 429 {
		t.Errorf("code requests were not limited: %d", last)
	}
	for i := 0; i < otpVerifiesPerMinute+2; i++ {
		resp, _ := p.call(t, "POST", "/auth/otp/verify", "", "", `{"email":"x@example.com","code":"000000"}`)
		last = resp.StatusCode
	}
	if last != 429 {
		t.Errorf("code tries were not limited: %d", last)
	}
}

func TestMethodsEndpoint(t *testing.T) {
	p := newProduct(t, nil, nil)
	_, m := p.call(t, "GET", "/auth/methods", "", "", "")
	if m["password"] != true || m["otp"] != true || m["google"] != false {
		t.Fatalf("without Google: %v", m)
	}
	p = newProduct(t, nil, func(d *Deps) {
		d.Google = oidc.New(oidc.Config{Issuer: "http://127.0.0.1:1", ClientID: "id", ClientSecret: "secret", RedirectURL: "http://x/cb"})
	})
	if _, m = p.call(t, "GET", "/auth/methods", "", "", ""); m["google"] != true {
		t.Fatalf("with Google: %v", m)
	}
	// Not configured: the routes answer 404 and say why.
	p = newProduct(t, nil, nil)
	if resp, m := p.call(t, "GET", "/auth/google/start", "", "", ""); resp.StatusCode != 404 || errKeyOf(m) != "identity.oauth.not_configured" {
		t.Errorf("start without Google: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "GET", "/auth/google/callback?code=x&state=y", "", "", ""); resp.StatusCode != 404 {
		t.Errorf("callback without Google: %d", resp.StatusCode)
	}
}

// ---- Google, against the in-repo provider ----

type googleRig struct {
	*product
	idp    *oidctest.Provider
	idpSrv *httptest.Server
	app    string
	web    *http.Client // follows nothing, so every redirect can be looked at
}

const (
	googleID     = "test-client.apps.example"
	googleSecret = "test-client-secret-not-real"
)

func newGoogleRig(t *testing.T) *googleRig {
	t.Helper()
	g := &googleRig{app: "http://console.test"}
	g.idp = oidctest.New(googleID, googleSecret, g.app+"/auth/google/callback")
	g.idpSrv = httptest.NewServer(g.idp)
	t.Cleanup(g.idpSrv.Close)
	g.idp.SetIssuer(g.idpSrv.URL)
	g.product = newProduct(t, nil, func(d *Deps) {
		d.AppURL = g.app
		d.Google = oidc.New(oidc.Config{Issuer: g.idpSrv.URL, ClientID: googleID, ClientSecret: googleSecret, RedirectURL: g.app + "/auth/google/callback"})
	})
	g.web = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return g
}

func (g *googleRig) get(t *testing.T, base, path string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", base+path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := g.web.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

// start runs /auth/google/start and the provider's authorize step, and returns the callback path Google would send the browser to, with the login cookie.
func (g *googleRig) start(t *testing.T) (callback string, cookie *http.Cookie, authorize *url.URL) {
	t.Helper()
	resp := g.get(t, g.ts.URL, "/auth/google/start?locale=de")
	if resp.StatusCode != 302 {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == googleCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.MaxAge <= 0 {
		t.Fatalf("login cookie missing or weak: %+v", cookie)
	}
	authorize, _ = url.Parse(resp.Header.Get("Location"))
	// The browser follows to the provider, which (with an account preset) redirects straight back.
	back := g.get(t, g.idpSrv.URL, authorize.RequestURI())
	if back.StatusCode != 302 {
		t.Fatalf("authorize: %d", back.StatusCode)
	}
	loc, _ := url.Parse(back.Header.Get("Location"))
	return loc.RequestURI(), cookie, authorize
}

func (g *googleRig) callback(t *testing.T, path string, cookie *http.Cookie) (reason string, frag url.Values) {
	t.Helper()
	var cs []*http.Cookie
	if cookie != nil {
		cs = append(cs, cookie)
	}
	resp := g.get(t, g.ts.URL, path, cs...)
	if resp.StatusCode != 302 {
		t.Fatalf("callback answered %d, want a redirect", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(resp.Header.Get("Location"), g.app+"/signin") {
		t.Fatalf("callback redirected to %q", resp.Header.Get("Location"))
	}
	if loc.Fragment != "" {
		frag, _ = url.ParseQuery(loc.Fragment)
		return "", frag
	}
	return loc.Query().Get("oauth_error"), nil
}

func TestGoogleHappyPath(t *testing.T) {
	g := newGoogleRig(t)
	g.idp.Account = &oidctest.Account{Email: "Ada@Example.com", Name: "Ada L", Subject: "sub-ada"}
	path, cookie, authz := g.start(t)
	q := authz.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("response_type") != "code" ||
		q.Get("client_id") != googleID || q.Get("redirect_uri") != g.app+"/auth/google/callback" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("authorization request: %v", q)
	}
	if strings.Contains(authz.String(), googleSecret) {
		t.Fatal("the client secret must never travel through the browser")
	}
	reason, frag := g.callback(t, path, cookie)
	if reason != "" || frag.Get("token") != tokAda || frag.Get("expires") == "" {
		t.Fatalf("callback: reason=%q fragment=%v", reason, frag)
	}
	got := g.ident.identities
	if len(got) != 1 || got[0].GetProvider() != "google" || got[0].GetSubject() != "sub-ada" || got[0].GetEmail() != "Ada@Example.com" || !got[0].GetEmailVerified() ||
		got[0].GetDisplayName() != "Ada L" || got[0].GetLocale() != "de" {
		t.Fatalf("plans-svc was asked: %v", got)
	}
}

func TestGoogleCallbackRefusals(t *testing.T) {
	for _, c := range []struct {
		name       string
		reason     string
		account    oidctest.Account
		tamper     func(map[string]any)
		forge      bool
		prep       func(t *testing.T, g *googleRig, path string, cookie *http.Cookie) (string, *http.Cookie)
		wantReason string
	}{
		{name: "a different state", prep: func(_ *testing.T, _ *googleRig, p string, ck *http.Cookie) (string, *http.Cookie) {
			return strings.Replace(p, "state=", "state=x", 1), ck
		}, wantReason: "state"},
		{name: "no cookie", prep: func(_ *testing.T, _ *googleRig, p string, _ *http.Cookie) (string, *http.Cookie) { return p, nil }, wantReason: "state"},
		{name: "a forged cookie", prep: func(_ *testing.T, _ *googleRig, p string, ck *http.Cookie) (string, *http.Cookie) {
			forged := *ck
			payload, _, _ := strings.Cut(ck.Value, ".")
			forged.Value = payload + "." + base64.RawURLEncoding.EncodeToString([]byte("not the signature"))
			return p, &forged
		}, wantReason: "state"},
		{name: "a cookie edited to match another state", prep: func(_ *testing.T, g *googleRig, p string, ck *http.Cookie) (string, *http.Cookie) {
			edited := *ck
			_, sig, _ := strings.Cut(ck.Value, ".")
			raw, _ := json.Marshal(loginState{State: "chosen-by-attacker", Nonce: "n", Verifier: "v", Expires: time.Now().Add(time.Hour).Unix()})
			edited.Value = base64.RawURLEncoding.EncodeToString(raw) + "." + sig
			return strings.Replace(p, "state=", "state=chosen-by-attacker&x=", 1), &edited
		}, wantReason: "state"},
		{name: "an expired cookie", prep: func(_ *testing.T, g *googleRig, p string, ck *http.Cookie) (string, *http.Cookie) {
			st, _ := g.srv().openState(ck.Value)
			st.Expires = time.Now().Add(-time.Minute).Unix()
			expired := *ck
			expired.Value = g.srv().sealState(st)
			return p, &expired
		}, wantReason: "state"},
		{name: "the user said no", prep: func(_ *testing.T, _ *googleRig, p string, ck *http.Cookie) (string, *http.Cookie) {
			u, _ := url.Parse(p)
			q := u.Query()
			q.Del("code")
			q.Set("error", "access_denied")
			return u.Path + "?" + q.Encode(), ck
		}, wantReason: "denied"},
		{name: "a wrong audience", tamper: func(m map[string]any) { m["aud"] = "someone-elses-client" }, wantReason: "token"},
		{name: "a wrong nonce", tamper: func(m map[string]any) { m["nonce"] = "replayed-from-another-login" }, wantReason: "token"},
		{name: "a wrong issuer", tamper: func(m map[string]any) { m["iss"] = "https://evil.example" }, wantReason: "token"},
		{name: "an expired token", tamper: func(m map[string]any) { m["exp"] = time.Now().Add(-time.Hour).Unix() }, wantReason: "token"},
		{name: "a token without a subject", tamper: func(m map[string]any) { m["sub"] = "" }, wantReason: "token"},
		{name: "several audiences without our azp", tamper: func(m map[string]any) { m["aud"] = []string{googleID, "other"}; m["azp"] = "other" }, wantReason: "token"},
		{name: "a forged signature", forge: true, wantReason: "token"},
		{name: "an unverified address", account: oidctest.Account{Email: "ada@example.com", Subject: "s1", Unverified: true}, wantReason: "email_unverified"},
		{name: "email_verified sent as a string that is not true", tamper: func(m map[string]any) { m["email_verified"] = "false" }, wantReason: "email_unverified"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newGoogleRig(t)
			acct := c.account
			if acct.Email == "" {
				acct = oidctest.Account{Email: "ada@example.com", Subject: "s1"}
			}
			g.idp.Account = &acct
			g.idp.Tamper = c.tamper
			if c.forge {
				k, _ := rsa.GenerateKey(rand.Reader, 2048)
				g.idp.SignWith = k
			}
			path, cookie, _ := g.start(t)
			if c.prep != nil {
				path, cookie = c.prep(t, g, path, cookie)
			}
			reason, frag := g.callback(t, path, cookie)
			if frag != nil || reason != c.wantReason {
				t.Fatalf("reason=%q fragment=%v, want %q and no session", reason, frag, c.wantReason)
			}
			if n := len(g.ident.identities); n != 0 {
				t.Fatalf("plans-svc was asked to sign somebody in after a refused login (%d calls)", n)
			}
		})
	}
}

func (g *googleRig) srv() *Server {
	// A server with the same key as the one under test: the cookie's key comes from the client secret.
	return &Server{d: Deps{Google: oidc.New(oidc.Config{Issuer: g.idpSrv.URL, ClientID: googleID, ClientSecret: googleSecret})}}
}

func TestGoogleCodeCannotBeReplayed(t *testing.T) {
	g := newGoogleRig(t)
	g.idp.Account = &oidctest.Account{Email: "ada@example.com", Subject: "s1"}
	path, cookie, _ := g.start(t)
	if reason, frag := g.callback(t, path, cookie); reason != "" || frag == nil {
		t.Fatalf("first use: %q", reason)
	}
	// The browser's back button: the same callback again, still holding the cookie it was given. The provider has spent the code.
	if reason, frag := g.callback(t, path, cookie); frag != nil || reason != "token" {
		t.Fatalf("replay: reason=%q fragment=%v", reason, frag)
	}
	if len(g.ident.identities) != 1 {
		t.Fatalf("a replay signed in again: %d", len(g.ident.identities))
	}
}

func TestGoogleRejectsAnUnsignedToken(t *testing.T) {
	// "alg": "none" with a plausible payload must never be accepted.
	g := newGoogleRig(t)
	c := oidc.New(oidc.Config{Issuer: g.idpSrv.URL, ClientID: googleID, ClientSecret: googleSecret})
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	tok := enc(map[string]string{"alg": "none", "kid": "oidctest-1"}) + "." +
		enc(map[string]any{"iss": g.idpSrv.URL, "sub": "x", "aud": googleID, "exp": time.Now().Add(time.Hour).Unix(), "nonce": "n", "email": "a@b.co", "email_verified": true}) + "."
	if _, err := c.Verify(context.Background(), tok, "n"); err == nil {
		t.Fatal("an unsigned token was accepted")
	}
	hs := enc(map[string]string{"alg": "HS256", "kid": "oidctest-1"}) + "." + enc(map[string]any{"sub": "x"}) + "." + enc("sig")
	if _, err := c.Verify(context.Background(), hs, "n"); err == nil {
		t.Fatal("a symmetric-algorithm token was accepted")
	}
}

func TestLoginCookieNeverCarriesTheSecret(t *testing.T) {
	g := newGoogleRig(t)
	g.idp.Account = &oidctest.Account{Email: "ada@example.com", Subject: "s1"}
	_, cookie, _ := g.start(t)
	if strings.Contains(cookie.Value, googleSecret) {
		t.Fatal("the client secret is in the cookie")
	}
	payload, _, _ := strings.Cut(cookie.Value, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	if strings.Contains(string(raw), googleSecret) {
		t.Fatal("the client secret is in the cookie payload")
	}
}
