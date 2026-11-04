package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// The OAuth authorization server's HTTP side (docs/plans/20-mcp.md): the metadata, the consent routes, the token endpoint and the qm_ credential on the tenant API. The
// identity service is the fake of product_test.go with the OAuth calls answered below; the client's metadata document comes from a fake transport.

const (
	oauthIssuer   = "https://api.example.com"
	oauthResource = "https://mcp.example.com/mcp"
	oauthClient   = "https://client.example/meta.json"
	oauthSecret   = "s3cret-s3cret-s3cret-s3cret-s3cret"
	oauthRedirect = "https://client.example/callback"
)

// oauthLog is what the fake identity service was asked and what it answers; the tests that use it do not run in parallel.
var oauthLog struct {
	sync.Mutex
	grants    []*plansv1.CreateOAuthGrantRequest
	exchanges []*plansv1.ExchangeOAuthCodeRequest
	mints     []*plansv1.MintOAuthApiTokenRequest
	active    map[string]*plansv1.OAuthTokenInfo // introspection and qm_ resolution, by token
}

func resetOAuthLog() {
	oauthLog.Lock()
	defer oauthLog.Unlock()
	oauthLog.grants, oauthLog.exchanges, oauthLog.mints = nil, nil, nil
	oauthLog.active = map[string]*plansv1.OAuthTokenInfo{}
}

func (f *fakeIdentity) CreateOAuthGrant(_ context.Context, req *plansv1.CreateOAuthGrantRequest) (*plansv1.CreateOAuthGrantResponse, error) {
	if req.GetTenantId() == adaLive && !req.GetAllowLive() {
		return nil, errkey.New(codes.PermissionDenied, "identity.oauth.live_disabled", "no")
	}
	oauthLog.Lock()
	oauthLog.grants = append(oauthLog.grants, req)
	oauthLog.Unlock()
	return &plansv1.CreateOAuthGrantResponse{Code: "code-123", Scopes: req.GetScopes()}, nil
}

func (f *fakeIdentity) ExchangeOAuthCode(_ context.Context, req *plansv1.ExchangeOAuthCodeRequest) (*plansv1.OAuthTokens, error) {
	oauthLog.Lock()
	oauthLog.exchanges = append(oauthLog.exchanges, req)
	oauthLog.Unlock()
	if req.GetCode() != "code-123" {
		return nil, errkey.New(codes.InvalidArgument, "identity.oauth.invalid_grant", "no")
	}
	return &plansv1.OAuthTokens{AccessToken: "qo_access", RefreshToken: "qr_refresh", ExpiresIn: durationpb.New(time.Hour), Scopes: []string{"customers:read"}}, nil
}

func (f *fakeIdentity) IntrospectOAuthToken(_ context.Context, req *plansv1.IntrospectOAuthTokenRequest) (*plansv1.OAuthTokenInfo, error) {
	oauthLog.Lock()
	defer oauthLog.Unlock()
	if info, ok := oauthLog.active[req.GetToken()]; ok && info.GetResource() == req.GetResource() {
		return info, nil
	}
	return &plansv1.OAuthTokenInfo{}, nil
}

func (f *fakeIdentity) MintOAuthApiToken(_ context.Context, req *plansv1.MintOAuthApiTokenRequest) (*plansv1.MintOAuthApiTokenResponse, error) {
	oauthLog.Lock()
	defer oauthLog.Unlock()
	oauthLog.mints = append(oauthLog.mints, req)
	info, ok := oauthLog.active[req.GetAccessToken()]
	if !ok {
		return nil, errkey.New(codes.Unauthenticated, "identity.oauth.invalid_token", "no")
	}
	return &plansv1.MintOAuthApiTokenResponse{Token: "qm_minted", ExpireTime: timestamppb.New(time.Now().Add(5 * time.Minute)), Info: info}, nil
}

func (f *fakeIdentity) ResolveOAuthApiToken(_ context.Context, req *plansv1.ResolveOAuthApiTokenRequest) (*plansv1.OAuthTokenInfo, error) {
	oauthLog.Lock()
	defer oauthLog.Unlock()
	if info, ok := oauthLog.active[req.GetToken()]; ok {
		return info, nil
	}
	return &plansv1.OAuthTokenInfo{}, nil
}

func tenantOf(id string) *plansv1.Tenant {
	for _, t := range appFor(adaApp).GetTenants() {
		if t.GetId() == id {
			return t
		}
	}
	return nil
}

// clientDoc serves the client's metadata document without a network: any other address is a 404.
type clientDoc struct {
	doc   string
	calls int
}

func (c *clientDoc) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls++
	if r.URL.String() != oauthClient {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(c.doc)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
}

func goodDoc() string {
	return `{"client_id":"` + oauthClient + `","client_name":"Example Assistant","redirect_uris":["` + oauthRedirect + `","http://localhost:3000/cb"],"token_endpoint_auth_method":"none"}`
}

func newOAuthProduct(t *testing.T, doc *clientDoc, allowLive bool) *product {
	t.Helper()
	resetOAuthLog()
	return newProduct(t, nil, func(d *Deps) {
		d.OAuth = &OAuth{Issuer: oauthIssuer, AppURL: "https://app.example.com", Resources: []string{oauthResource}, ResourceSecret: oauthSecret, AllowLive: allowLive,
			Client: &http.Client{Transport: doc}}
	})
}

func challenge(verifier string) string {
	s := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

func authzQuery(over map[string]string) string {
	q := url.Values{"client_id": {oauthClient}, "redirect_uri": {oauthRedirect}, "response_type": {"code"}, "code_challenge": {challenge(strings.Repeat("v", 43))},
		"code_challenge_method": {"S256"}, "scope": {"customers:read invoices:read"}, "resource": {oauthResource}, "state": {"xyz"}}
	for k, v := range over {
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
	}
	return q.Encode()
}

func oauthPost(t *testing.T, p *product, path, bearer string, f url.Values) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", p.ts.URL+path, strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func TestOAuthMetadata(t *testing.T) {
	p := newOAuthProduct(t, &clientDoc{doc: goodDoc()}, false)
	resp, m := p.call(t, "GET", "/.well-known/oauth-authorization-server", "", "", "")
	if resp.StatusCode != 200 || m["issuer"] != oauthIssuer || m["token_endpoint"] != oauthIssuer+"/oauth/token" || m["authorization_endpoint"] != "https://app.example.com/oauth/authorize" {
		t.Fatalf("metadata = %d %v", resp.StatusCode, m)
	}
	if m["client_id_metadata_document_supported"] != true || m["authorization_response_iss_parameter_supported"] != true {
		t.Errorf("CIMD and iss support not advertised: %v", m)
	}
	if got := m["code_challenge_methods_supported"].([]any); len(got) != 1 || got[0] != "S256" {
		t.Errorf("PKCE methods = %v", got)
	}
	if _, has := m["registration_endpoint"]; has {
		t.Error("dynamic registration is not built and must not be advertised")
	}
	off := newProduct(t, nil, nil)
	if resp, m := off.call(t, "GET", "/.well-known/oauth-authorization-server", "", "", ""); resp.StatusCode != 404 || errKeyOf(m) != "identity.oauth.disabled" {
		t.Errorf("off: %d %v", resp.StatusCode, m)
	}
	if resp, _ := oauthPost(t, off, "/oauth/token", "", url.Values{"grant_type": {"authorization_code"}}); resp.StatusCode != 404 {
		t.Errorf("the token endpoint answers %d while off", resp.StatusCode)
	}
}

func TestOAuthAuthorizationRequestIsValidatedAndNeverRedirectsAnError(t *testing.T) {
	doc := &clientDoc{doc: goodDoc()}
	p := newOAuthProduct(t, doc, false)
	resp, m := p.call(t, "GET", "/app/v1/oauth/request?"+authzQuery(nil), tokAda, "", "")
	if resp.StatusCode != 200 || m["client_name"] != "Example Assistant" || m["client_host"] != "client.example" || m["redirect_host"] != "client.example" || m["resource"] != oauthResource {
		t.Fatalf("describe = %d %v", resp.StatusCode, m)
	}
	if scopes := m["scopes"].([]any); len(scopes) != 2 {
		t.Errorf("scopes = %v", scopes)
	}
	// The same client asked about again is read from memory.
	before := doc.calls
	p.call(t, "GET", "/app/v1/oauth/request?"+authzQuery(nil), tokAda, "", "")
	if doc.calls != before {
		t.Error("the client document was fetched again")
	}
	if resp, _ := p.call(t, "GET", "/app/v1/oauth/request?"+authzQuery(nil), "", "", ""); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
	for name, over := range map[string]map[string]string{
		"no PKCE":                 {"code_challenge": "", "code_challenge_method": ""},
		"plain PKCE":              {"code_challenge_method": "plain"},
		"a malformed challenge":   {"code_challenge": "short"},
		"another response type":   {"response_type": "token"},
		"a redirect not listed":   {"redirect_uri": "https://evil.example/callback"},
		"a redirect with a query": {"redirect_uri": oauthRedirect + "?x=1"},
		"http redirect":           {"redirect_uri": "http://client.example/callback"},
		"no resource":             {"resource": ""},
		"another resource":        {"resource": "https://other.example/mcp"},
		"an unknown scope":        {"scope": "customers:read root"},
		"a client_id not https":   {"client_id": "http://client.example/meta.json"},
		"a client_id that 404s":   {"client_id": "https://client.example/other.json"},
		"a huge state":            {"state": strings.Repeat("s", oauthStateMax+1)},
	} {
		resp, m := p.call(t, "GET", "/app/v1/oauth/request?"+authzQuery(over), tokAda, "", "")
		if resp.StatusCode != 400 || errKeyOf(m) != "identity.oauth.bad_request" || resp.Header.Get("Location") != "" {
			t.Errorf("%s: %d %v", name, resp.StatusCode, m)
		}
	}
	// A loopback redirect may use another port than the one listed.
	if resp, m := p.call(t, "GET", "/app/v1/oauth/request?"+authzQuery(map[string]string{"redirect_uri": "http://localhost:51234/cb"}), tokAda, "", ""); resp.StatusCode != 200 || m["redirect_loopback"] != true {
		t.Errorf("loopback: %d %v", resp.StatusCode, m)
	}
	// A document that names another client_id is refused.
	bad := newOAuthProduct(t, &clientDoc{doc: strings.Replace(goodDoc(), oauthClient, "https://evil.example/m.json", 1)}, false)
	if resp, _ := bad.call(t, "GET", "/app/v1/oauth/request?"+authzQuery(nil), tokAda, "", ""); resp.StatusCode != 400 {
		t.Errorf("a document of another client was accepted: %d", resp.StatusCode)
	}
}

func consentBody(t *testing.T, over map[string]any) string {
	t.Helper()
	b := map[string]any{"client_id": oauthClient, "redirect_uri": oauthRedirect + "?keep=1", "response_type": "code", "code_challenge": challenge(strings.Repeat("v", 43)),
		"code_challenge_method": "S256", "scope": "customers:read invoices:read", "resource": oauthResource, "state": "xyz", "decision": "allow", "tenant_id": adaSandbox}
	for k, v := range over {
		b[k] = v
	}
	raw, _ := json.Marshal(b)
	return string(raw)
}

func TestOAuthConsent(t *testing.T) {
	doc := &clientDoc{doc: strings.Replace(goodDoc(), oauthRedirect+`"`, oauthRedirect+`?keep=1"`, 1)}
	p := newOAuthProduct(t, doc, false)
	resp, m := p.call(t, "POST", "/app/v1/oauth/consent", tokAda, "", consentBody(t, nil))
	if resp.StatusCode != 200 {
		t.Fatalf("allow = %d %v", resp.StatusCode, m)
	}
	u, err := url.Parse(m["redirect_to"].(string))
	if err != nil || u.Host != "client.example" || u.Path != "/callback" || u.Query().Get("code") != "code-123" || u.Query().Get("state") != "xyz" || u.Query().Get("iss") != oauthIssuer || u.Query().Get("keep") != "1" {
		t.Fatalf("redirect_to = %v", m["redirect_to"])
	}
	oauthLog.Lock()
	g := oauthLog.grants[0]
	oauthLog.Unlock()
	if g.GetUserId() != adaUser || g.GetTenantId() != adaSandbox || g.GetClientId() != oauthClient || g.GetResource() != oauthResource || len(g.GetScopes()) != 2 {
		t.Errorf("grant = %v", g)
	}
	// The person takes a scope away: the grant has one.
	p.call(t, "POST", "/app/v1/oauth/consent", tokAda, "", consentBody(t, map[string]any{"scopes": []string{"customers:read"}}))
	oauthLog.Lock()
	narrowed := oauthLog.grants[1].GetScopes()
	oauthLog.Unlock()
	if len(narrowed) != 1 || narrowed[0] != "customers:read" {
		t.Errorf("narrowed scopes = %v", narrowed)
	}
	// ...but cannot add one that was not asked for.
	if resp, _ := p.call(t, "POST", "/app/v1/oauth/consent", tokAda, "", consentBody(t, map[string]any{"scopes": []string{"customers:write"}})); resp.StatusCode != 400 {
		t.Errorf("a scope not asked for: %d", resp.StatusCode)
	}
	// Deny answers with access_denied to the redirect URI, with state and iss, and creates nothing.
	resp, m = p.call(t, "POST", "/app/v1/oauth/consent", tokAda, "", consentBody(t, map[string]any{"decision": "deny"}))
	u, _ = url.Parse(m["redirect_to"].(string))
	if resp.StatusCode != 200 || u.Query().Get("error") != "access_denied" || u.Query().Get("state") != "xyz" || u.Query().Get("code") != "" {
		t.Errorf("deny = %d %v", resp.StatusCode, m)
	}
	oauthLog.Lock()
	n := len(oauthLog.grants)
	oauthLog.Unlock()
	if n != 2 {
		t.Errorf("%d grants, want 2", n)
	}
	// An invalid request is not redirected, and nothing is created.
	if resp, m := p.call(t, "POST", "/app/v1/oauth/consent", tokAda, "", consentBody(t, map[string]any{"redirect_uri": "https://evil.example/cb"})); resp.StatusCode != 400 || m["redirect_to"] != nil {
		t.Errorf("a redirect not listed: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", "/app/v1/oauth/consent", tokAda, "", consentBody(t, map[string]any{"decision": "maybe"})); resp.StatusCode != 400 {
		t.Errorf("a decision that is neither: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "POST", "/app/v1/oauth/consent", "", "", consentBody(t, nil)); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
	// A live environment is refused while the deployment has not switched it on (plans-svc also checks the address); on, it goes through.
	if resp, m := p.call(t, "POST", "/app/v1/oauth/consent", tokAda, "", consentBody(t, map[string]any{"tenant_id": adaLive})); resp.StatusCode != 403 || errKeyOf(m) != "identity.oauth.live_disabled" {
		t.Errorf("live while off: %d %v", resp.StatusCode, m)
	}
	live := newOAuthProduct(t, doc, true)
	if resp, _ := live.call(t, "POST", "/app/v1/oauth/consent", tokAda, "", consentBody(t, map[string]any{"tenant_id": adaLive})); resp.StatusCode != 200 {
		t.Errorf("live while on: %d", resp.StatusCode)
	}
}

func TestOAuthTokenEndpoint(t *testing.T) {
	p := newOAuthProduct(t, &clientDoc{doc: goodDoc()}, false)
	form := url.Values{"grant_type": {"authorization_code"}, "code": {"code-123"}, "client_id": {oauthClient}, "redirect_uri": {oauthRedirect}, "code_verifier": {strings.Repeat("v", 43)}, "resource": {oauthResource}}
	resp, m := oauthPost(t, p, "/oauth/token", "", form)
	if resp.StatusCode != 200 || m["access_token"] != "qo_access" || m["refresh_token"] != "qr_refresh" || m["token_type"] != "Bearer" || m["expires_in"] != float64(3600) || m["scope"] != "customers:read" {
		t.Fatalf("token = %d %v", resp.StatusCode, m)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("a token answer must not be cached")
	}
	oauthLog.Lock()
	ex := oauthLog.exchanges[0]
	oauthLog.Unlock()
	if ex.GetCodeVerifier() != strings.Repeat("v", 43) || ex.GetRedirectUri() != oauthRedirect || ex.GetClientId() != oauthClient {
		t.Errorf("exchange = %v", ex)
	}
	// A wrong code is RFC 6749's invalid_grant, not the API's error body.
	form.Set("code", "wrong")
	if resp, m := oauthPost(t, p, "/oauth/token", "", form); resp.StatusCode != 400 || m["error"] != "invalid_grant" {
		t.Errorf("wrong code: %d %v", resp.StatusCode, m)
	}
	// A token in the query string is not read (only the form body is).
	req, _ := http.NewRequest("POST", p.ts.URL+"/oauth/token?grant_type=authorization_code&code=code-123", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 400 {
		t.Errorf("query parameters were read: %v", r)
	}
	if resp, m := oauthPost(t, p, "/oauth/token", "", url.Values{"grant_type": {"password"}}); resp.StatusCode != 400 || m["error"] != "unsupported_grant_type" {
		t.Errorf("password grant: %d %v", resp.StatusCode, m)
	}
	// The endpoint is rate limited per address: 30 requests a minute.
	limited := false
	for i := 0; i < oauthTokensPerMinute+2; i++ {
		if resp, m := oauthPost(t, p, "/oauth/token", "", url.Values{"grant_type": {"password"}}); resp.StatusCode == 429 {
			limited = m["error"] == "slow_down" && resp.Header.Get("Retry-After") != ""
			break
		}
	}
	if !limited {
		t.Error("the token endpoint was not rate limited")
	}
}

func TestOAuthIntrospectionAndExchangeNeedTheResourceSecret(t *testing.T) {
	p := newOAuthProduct(t, &clientDoc{doc: goodDoc()}, false)
	info := &plansv1.OAuthTokenInfo{Active: true, GrantId: "g1", UserId: adaUser, UserEmail: "ada@example.com", Tenant: tenantOf(adaSandbox), MemberRole: "owner", Scopes: []string{"customers:read"},
		ClientId: oauthClient, ClientName: "Example Assistant", Resource: oauthResource, ExpireTime: timestamppb.New(time.Now().Add(time.Hour))}
	oauthLog.Lock()
	oauthLog.active["qo_good"] = info
	oauthLog.Unlock()
	in := url.Values{"token": {"qo_good"}, "resource": {oauthResource}}
	if resp, m := oauthPost(t, p, "/oauth/introspect", "", in); resp.StatusCode != 401 || m["error"] != "invalid_client" {
		t.Errorf("without the secret: %d %v", resp.StatusCode, m)
	}
	if resp, _ := oauthPost(t, p, "/oauth/introspect", "wrong-secret", in); resp.StatusCode != 401 {
		t.Errorf("wrong secret: %d", resp.StatusCode)
	}
	resp, m := oauthPost(t, p, "/oauth/introspect", oauthSecret, in)
	if resp.StatusCode != 200 || m["active"] != true || m["scope"] != "customers:read" || m["aud"] != oauthResource || m["environment"] != "sandbox" || m["tenant_id"] != adaSandbox || m["client_id"] != oauthClient {
		t.Fatalf("introspect = %d %v", resp.StatusCode, m)
	}
	// A token issued for another resource, and an unknown one, are inactive.
	if _, m := oauthPost(t, p, "/oauth/introspect", oauthSecret, url.Values{"token": {"qo_good"}, "resource": {"https://other.example/mcp"}}); m["active"] != false {
		t.Errorf("another resource: %v", m)
	}
	if _, m := oauthPost(t, p, "/oauth/introspect", oauthSecret, url.Values{"token": {"qo_nope"}, "resource": {oauthResource}}); m["active"] != false || len(m) != 1 {
		t.Errorf("unknown token: %v", m)
	}
	// The exchange: only with the secret, only for this API as the audience.
	ex := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"}, "subject_token": {"qo_good"}, "subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience": {oauthIssuer}, "resource": {oauthResource}}
	if resp, _ := oauthPost(t, p, "/oauth/token", "", ex); resp.StatusCode != 401 {
		t.Errorf("exchange without the secret: %d", resp.StatusCode)
	}
	bad := url.Values{}
	for k, v := range ex {
		bad[k] = v
	}
	bad.Set("audience", "https://elsewhere.example")
	if resp, m := oauthPost(t, p, "/oauth/token", oauthSecret, bad); resp.StatusCode != 400 || m["error"] != "invalid_target" {
		t.Errorf("another audience: %d %v", resp.StatusCode, m)
	}
	resp, m = oauthPost(t, p, "/oauth/token", oauthSecret, ex)
	if resp.StatusCode != 200 || m["access_token"] != "qm_minted" || m["scope"] != "customers:read" {
		t.Fatalf("exchange = %d %v", resp.StatusCode, m)
	}
	// A live environment's token is not exchanged while the deployment has live off (it may have been switched off since consent).
	liveInfo := &plansv1.OAuthTokenInfo{Active: true, Tenant: tenantOf(adaLive), Scopes: []string{"customers:read"}, Resource: oauthResource, ExpireTime: info.ExpireTime}
	oauthLog.Lock()
	oauthLog.active["qo_live"] = liveInfo
	oauthLog.Unlock()
	ex.Set("subject_token", "qo_live")
	if resp, m := oauthPost(t, p, "/oauth/token", oauthSecret, ex); resp.StatusCode != 403 || m["error"] != "access_denied" {
		t.Errorf("live exchange while off: %d %v", resp.StatusCode, m)
	}
}

func TestOAuthCredentialOnTheTenantAPI(t *testing.T) {
	p := newOAuthProduct(t, &clientDoc{doc: goodDoc()}, false)
	mk := func(env string, scopes ...string) *plansv1.OAuthTokenInfo {
		return &plansv1.OAuthTokenInfo{Active: true, GrantId: "g1", UserId: adaUser, UserEmail: "ada@example.com", Tenant: tenantOf(env), MemberRole: "owner", Scopes: scopes,
			ClientId: oauthClient, ClientName: "Example Assistant", Resource: oauthResource}
	}
	oauthLog.Lock()
	oauthLog.active["qm_write"] = mk(adaSandbox, "customers:write")
	oauthLog.active["qm_read"] = mk(adaSandbox, "customers:read")
	oauthLog.active["qm_live"] = mk(adaLive, "customers:read")
	oauthLog.Unlock()
	body := `{"external_id":"c1","name":"C","email":"c@example.com"}`
	// The scopes are the permissions of a custom key: a write scope creates a customer, a read scope does not.
	if resp, m := p.call(t, "POST", "/v1/customers", "qm_write", "", body); resp.StatusCode == 401 || resp.StatusCode == 403 {
		t.Errorf("customers:write could not create a customer: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "POST", "/v1/customers", "qm_read", "", body); resp.StatusCode != 403 || errKeyOf(m) != "gateway.auth.permission_denied" {
		t.Errorf("customers:read created a customer: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "GET", "/v1/customers", "qm_write", "", ""); resp.StatusCode != 403 {
		t.Errorf("customers:write read customers: %d %v", resp.StatusCode, m)
	}
	// A route no permission covers needs an admin key: a custom credential cannot reach it.
	if resp, _ := p.call(t, "GET", "/v1/api-keys", "qm_read", "", ""); resp.StatusCode != 403 && resp.StatusCode != 404 {
		t.Errorf("an uncovered route: %d", resp.StatusCode)
	}
	// An unknown credential, the assistant's own access token and a credential of a live environment while live is off are refused.
	if resp, m := p.call(t, "GET", "/v1/customers", "qm_unknown", "", ""); resp.StatusCode != 401 || errKeyOf(m) != "identity.api_key.invalid" {
		t.Errorf("unknown credential: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "GET", "/v1/customers", "qo_access", "", ""); resp.StatusCode != 401 {
		t.Errorf("an access token reached the API: %d", resp.StatusCode)
	}
	if resp, m := p.call(t, "GET", "/v1/customers", "qm_live", "", ""); resp.StatusCode != 403 || errKeyOf(m) != "identity.oauth.live_disabled" {
		t.Errorf("live credential while off: %d %v", resp.StatusCode, m)
	}
	// With OAuth off the prefix is just an invalid key.
	off := newProduct(t, nil, nil)
	if resp, _ := off.call(t, "GET", "/v1/customers", "qm_write", "", ""); resp.StatusCode != 401 {
		t.Errorf("OAuth off: %d", resp.StatusCode)
	}
	// An access token is not a session: the console's routes refuse it.
	if resp, _ := p.call(t, "GET", "/app/v1/oauth/grants", "qm_write", "", ""); resp.StatusCode != 401 {
		t.Errorf("a credential opened a console route: %d", resp.StatusCode)
	}
}
