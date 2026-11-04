package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/apikey"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/oauth"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/auth"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// OAuth is the gateway's side of the OAuth 2.1 authorization server (docs/plans/20-mcp.md, "OAuth design"; default, not agreed): the metadata, the token, revocation and
// introspection endpoints under /oauth/*, the two routes the console's consent page calls, and the acceptance of the short-lived credential (qm_) that the MCP server gets
// in exchange for an access token. Grants, codes and tokens live in plans-svc. Nil in Deps.OAuth: off, every route answers 404.
type OAuth struct {
	// Issuer is the gateway's public address (PAYRUDDER_PUBLIC_URL), without a path: the metadata is served at its /.well-known/oauth-authorization-server.
	Issuer string
	// AppURL is the console's address; its /oauth/authorize is the authorization endpoint.
	AppURL string
	// Resources are the canonical URLs of the MCP servers a token may be issued for (PAYRUDDER_OAUTH_RESOURCES); any other `resource` is refused.
	Resources []string
	// ResourceSecret (PAYRUDDER_OAUTH_RESOURCE_SECRET) authenticates the MCP server at the introspection endpoint and for the token exchange.
	ResourceSecret string
	// AllowLive (PAYRUDDER_OAUTH_ALLOW_LIVE) lets a person consent to an assistant acting on a live environment; off by default.
	AllowLive bool
	// Client fetches Client ID Metadata Documents; it must refuse non-public addresses (pkg/safehttp). Nil: net/http's default, for tests.
	Client *http.Client
}

// Per-address limits of the endpoints without a session: the token endpoint (and revocation) is open to anyone, the resource endpoints to whoever holds the secret.
const (
	oauthTokensPerMinute    = 30
	oauthResourcePerMinute  = 1200
	oauthStateMax           = 2048
	oauthClientDocTTL       = 5 * time.Minute
	oauthClientCacheMax     = 1000
	oauthClientFetchTimeout = 5 * time.Second
)

func (s *Server) oauthRoutes() []Route {
	return []Route{
		{"GET", "/.well-known/oauth-authorization-server", AccessPublic, "", s.oauthMetadata},
		{"POST", "/oauth/token", AccessPublic, "", s.oauthToken},
		{"POST", "/oauth/revoke", AccessPublic, "", s.oauthRevoke},
		{"POST", "/oauth/introspect", AccessPublic, "", s.oauthIntrospect},
		{"GET", "/app/v1/oauth/request", AccessSession, ratelimit.ClassRead, s.oauthDescribe},
		{"POST", "/app/v1/oauth/consent", AccessSession, ratelimit.ClassWrite, s.oauthConsent},
		{"GET", "/app/v1/oauth/grants", AccessSession, ratelimit.ClassRead, s.oauthGrants},
		{"DELETE", "/app/v1/oauth/grants/{id}", AccessSession, ratelimit.ClassWrite, s.oauthRevokeGrant},
	}
}

func (s *Server) oauthOn(w http.ResponseWriter, r *http.Request) bool {
	if s.d.OAuth != nil {
		return true
	}
	writeError(w, r, errkey.New(codes.NotFound, "identity.oauth.disabled", "this deployment does not offer OAuth sign-in to assistants"))
	return false
}

// oauthMetadata is RFC 8414. client_id_metadata_document_supported and the iss parameter (RFC 9207) are what the MCP specification asks an authorization server to say.
func (s *Server) oauthMetadata(w http.ResponseWriter, r *http.Request) {
	if !s.oauthOn(w, r) {
		return
	}
	o := s.d.OAuth
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSONValue(w, http.StatusOK, map[string]any{
		"issuer":                                         o.Issuer,
		"authorization_endpoint":                         o.AppURL + "/oauth/authorize",
		"token_endpoint":                                 o.Issuer + "/oauth/token",
		"revocation_endpoint":                            o.Issuer + "/oauth/revoke",
		"introspection_endpoint":                         o.Issuer + "/oauth/introspect",
		"scopes_supported":                               oauth.Scopes(),
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"revocation_endpoint_auth_methods_supported":     []string{"none"},
		"introspection_endpoint_auth_methods_supported":  []string{"bearer"},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	})
}

// ---- the authorization request (the console's consent page) ----

type authzRequest struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Scope               string `json:"scope"`
	Resource            string `json:"resource"`
	State               string `json:"state"`
}

func authzFromQuery(q url.Values) authzRequest {
	return authzRequest{ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), ResponseType: q.Get("response_type"), CodeChallenge: q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"), Scope: q.Get("scope"), Resource: q.Get("resource"), State: q.Get("state")}
}

func badAuthz(msg string) error {
	return errkey.New(codes.InvalidArgument, "identity.oauth.bad_request", msg)
}

// check validates an authorization request the way the specification and OAuth 2.1 ask: the client is read from its document, the redirect URI must be one the document
// lists (exact, except a loopback port), PKCE S256 is required, the resource must be one of this deployment's MCP servers, and every scope must be known. A bad request is
// never answered by redirecting to the redirect URI (the console shows it): no error can be turned into an open redirect.
func (s *Server) checkAuthz(ctx context.Context, a authzRequest) (c oauth.Client, resource string, perms []string, err error) {
	o := s.d.OAuth
	switch {
	case a.ResponseType != "code":
		return c, "", nil, badAuthz("response_type must be code")
	case a.CodeChallengeMethod != "S256" || !oauth.ValidChallenge(a.CodeChallenge):
		return c, "", nil, badAuthz("PKCE with code_challenge_method S256 and a code_challenge is required")
	case len(a.State) > oauthStateMax:
		return c, "", nil, badAuthz("state is too long")
	}
	resource, ok := oauth.Resource(a.Resource)
	if !ok || !slices.Contains(o.Resources, resource) {
		return c, "", nil, badAuthz("resource is not an MCP server of this deployment")
	}
	if c, err = s.clientOf(ctx, a.ClientID); err != nil {
		return c, "", nil, err
	}
	if !oauth.RedirectMatches(c.RedirectURIs, a.RedirectURI) {
		return c, "", nil, badAuthz("redirect_uri is not one the client registered")
	}
	perms, unknown := oauth.ParseScope(a.Scope)
	if len(unknown) > 0 || len(perms) == 0 {
		return c, "", nil, badAuthz("unknown scope " + strings.Join(unknown, " "))
	}
	return c, resource, perms, nil
}

// oauthDescribe is what the consent page shows before the person decides: who is asking, where the answer goes, what for and what it may do.
func (s *Server) oauthDescribe(w http.ResponseWriter, r *http.Request) {
	if !s.oauthOn(w, r) {
		return
	}
	a := authzFromQuery(r.URL.Query())
	c, resource, perms, err := s.checkAuthz(r.Context(), a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	cu, _ := url.Parse(c.ID)
	ru, _ := url.Parse(a.RedirectURI)
	noStore(w)
	writeJSONValue(w, http.StatusOK, map[string]any{
		"client_name": c.Name, "client_id": c.ID, "client_host": cu.Host, "redirect_host": ru.Host, "redirect_loopback": oauth.IsLoopbackRedirect(a.RedirectURI),
		"resource": resource, "scopes": perms, "allow_live": s.d.OAuth.AllowLive,
	})
}

// oauthConsent records the person's decision. It validates the request again (the page's copy is not trusted), then answers with the address the browser goes to: the
// client's redirect URI with the code, or with access_denied, and always with state and iss (RFC 9207). The session is the person; the tenant must be one of their accounts.
func (s *Server) oauthConsent(w http.ResponseWriter, r *http.Request) {
	if !s.oauthOn(w, r) {
		return
	}
	var body struct {
		authzRequest
		TenantID string   `json:"tenant_id"`
		Scopes   []string `json:"scopes"`
		Decision string   `json:"decision"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	c, resource, perms, err := s.checkAuthz(r.Context(), body.authzRequest)
	if err != nil {
		writeError(w, r, err)
		return
	}
	back := func(p map[string]string) {
		p["state"], p["iss"] = body.State, s.d.OAuth.Issuer
		noStore(w)
		writeJSONValue(w, http.StatusOK, map[string]string{"redirect_to": withParams(body.RedirectURI, p)})
	}
	switch body.Decision {
	case "deny":
		back(map[string]string{"error": "access_denied"})
		return
	case "allow":
	default:
		writeError(w, r, badAuthz("decision must be allow or deny"))
		return
	}
	// The person may take away scopes, never add: what they keep is inside what was asked.
	chosen := perms
	if body.Scopes != nil {
		chosen = nil
		for _, p := range body.Scopes {
			if !slices.Contains(perms, p) {
				writeError(w, r, badAuthz("scope "+p+" was not asked for"))
				return
			}
			chosen = append(chosen, p)
		}
	}
	sess := sessionFrom(r.Context())
	resp, err := s.d.Identity.CreateOAuthGrant(r.Context(), &plansv1.CreateOAuthGrantRequest{UserId: sess.UserID, TenantId: body.TenantID, ClientId: c.ID, ClientName: c.Name,
		RedirectUri: body.RedirectURI, CodeChallenge: body.CodeChallenge, Resource: resource, Scopes: chosen, AllowLive: s.d.OAuth.AllowLive})
	if err != nil {
		writeError(w, r, err)
		return
	}
	back(map[string]string{"code": resp.GetCode()})
}

// withParams adds query parameters to a redirect URI that is already validated, keeping what it has; an empty value is left out.
func withParams(redirectURI string, params map[string]string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Server) oauthGrants(w http.ResponseWriter, r *http.Request) {
	if !s.oauthOn(w, r) {
		return
	}
	resp, err := s.d.Identity.ListMyOAuthGrants(r.Context(), &plansv1.ListMyOAuthGrantsRequest{UserId: sessionFrom(r.Context()).UserID})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) oauthRevokeGrant(w http.ResponseWriter, r *http.Request) {
	if !s.oauthOn(w, r) {
		return
	}
	if _, err := s.d.Identity.RevokeMyOAuthGrant(r.Context(), &plansv1.RevokeMyOAuthGrantRequest{UserId: sessionFrom(r.Context()).UserID, GrantId: r.PathValue("id")}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Client ID Metadata Documents ----

type clientEntry struct {
	c   oauth.Client
	exp time.Time
}

// clientCache remembers the documents read, for a few minutes: the same client is asked about again and again while a person is deciding.
type clientCache struct {
	mu sync.Mutex
	m  map[string]clientEntry
}

// clientOf reads the client's metadata document from its client_id URL. The fetch goes through the SSRF-safe client (no private address, no redirect), is cut at
// oauth.MaxClientDocument bytes and times out; a failure is a bad request that says why, because the person who starts the sign-in is the client's own user.
func (s *Server) clientOf(ctx context.Context, clientID string) (oauth.Client, error) {
	if !oauth.ClientIDURL(clientID) {
		return oauth.Client{}, badAuthz("client_id must be the https address of the client's metadata document")
	}
	now := s.now()
	s.clients.mu.Lock()
	e, hit := s.clients.m[clientID]
	s.clients.mu.Unlock()
	if hit && now.Before(e.exp) {
		return e.c, nil
	}
	c, err := s.fetchClient(ctx, clientID)
	if err != nil {
		return oauth.Client{}, badAuthz("the client's metadata document could not be used: " + err.Error())
	}
	s.clients.mu.Lock()
	if s.clients.m == nil || len(s.clients.m) >= oauthClientCacheMax {
		s.clients.m = map[string]clientEntry{}
	}
	s.clients.m[clientID] = clientEntry{c: c, exp: now.Add(oauthClientDocTTL)}
	s.clients.mu.Unlock()
	return c, nil
}

func (s *Server) fetchClient(ctx context.Context, clientID string) (oauth.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthClientFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return oauth.Client{}, err
	}
	req.Header.Set("Accept", "application/json")
	hc := s.d.OAuth.Client
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return oauth.Client{}, errors.New("it could not be fetched")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return oauth.Client{}, fmt.Errorf("it answered %d", resp.StatusCode)
	}
	doc, err := io.ReadAll(io.LimitReader(resp.Body, oauth.MaxClientDocument+1))
	if err != nil {
		return oauth.Client{}, errors.New("it could not be read")
	}
	return oauth.ParseClient(clientID, doc)
}

// ---- the token endpoint and its neighbours ----

// oauthFail writes an RFC 6749 error. Nothing about the cause beyond the code and a fixed description: a caller learns no more from a wrong code than from an expired one.
func oauthFail(w http.ResponseWriter, status int, code, desc string) {
	noStore(w)
	writeJSONValue(w, status, map[string]string{"error": code, "error_description": desc})
}

// oauthFailFrom turns a plans-svc refusal into the OAuth error it stands for.
func oauthFailFrom(w http.ResponseWriter, err error) {
	switch status.Code(err) {
	case codes.InvalidArgument:
		oauthFail(w, http.StatusBadRequest, "invalid_grant", "the code or refresh token is invalid, expired or was already used")
	case codes.Unauthenticated:
		oauthFail(w, http.StatusUnauthorized, "invalid_token", "the token is invalid, expired or revoked")
	case codes.Unavailable, codes.DeadlineExceeded:
		oauthFail(w, http.StatusServiceUnavailable, "temporarily_unavailable", "try again in a moment")
	default:
		oauthFail(w, http.StatusInternalServerError, "server_error", "the server failed")
	}
}

// oauthForm checks the method's usual preconditions of a public OAuth endpoint (switched on, within the address's limit, a form body) and reads the form.
func (s *Server) oauthForm(w http.ResponseWriter, r *http.Request, l *hostedLimiter, perMinute int) (url.Values, bool) {
	if !s.oauthOn(w, r) {
		return nil, false
	}
	if !l.allowN(s.limitAddr(r), s.now(), perMinute) {
		w.Header().Set("Retry-After", retryAfterMinute)
		oauthFail(w, http.StatusTooManyRequests, "slow_down", "too many requests from this address: try again in a minute")
		return nil, false
	}
	if err := r.ParseForm(); err != nil || r.PostForm == nil {
		oauthFail(w, http.StatusBadRequest, "invalid_request", "the body must be application/x-www-form-urlencoded")
		return nil, false
	}
	return r.PostForm, true
}

// resourceAuthed says whether the request carries the MCP servers' secret.
func (s *Server) resourceAuthed(r *http.Request) bool {
	secret := s.d.OAuth.ResourceSecret
	got := bearerToken(r)
	return secret != "" && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	f, ok := s.oauthForm(w, r, &s.limits.oauthTokens, oauthTokensPerMinute)
	if !ok {
		return
	}
	noStore(w)
	switch f.Get("grant_type") {
	case "authorization_code":
		resp, err := s.d.Identity.ExchangeOAuthCode(r.Context(), &plansv1.ExchangeOAuthCodeRequest{Code: f.Get("code"), ClientId: f.Get("client_id"), RedirectUri: f.Get("redirect_uri"),
			CodeVerifier: f.Get("code_verifier"), Resource: f.Get("resource")})
		s.oauthTokens(w, resp, err)
	case "refresh_token":
		resp, err := s.d.Identity.RefreshOAuthToken(r.Context(), &plansv1.RefreshOAuthTokenRequest{RefreshToken: f.Get("refresh_token"), ClientId: f.Get("client_id"), Resource: f.Get("resource")})
		s.oauthTokens(w, resp, err)
	case oauth.GrantTypeExchange:
		s.oauthExchange(w, r, f)
	default:
		oauthFail(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (s *Server) oauthTokens(w http.ResponseWriter, resp *plansv1.OAuthTokens, err error) {
	if err != nil {
		oauthFailFrom(w, err)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"access_token": resp.GetAccessToken(), "token_type": "Bearer", "expires_in": int64(resp.GetExpiresIn().AsDuration().Seconds()),
		"refresh_token": resp.GetRefreshToken(), "scope": strings.Join(resp.GetScopes(), " ")})
}

// oauthExchange is RFC 8693 for the one exchange this server offers: an MCP server (it proves it by the secret) trades an access token that was issued for it for a
// credential for the API audience. `audience` is the API (this gateway's issuer address), `resource` is the MCP server's own canonical address (an extension: the audience of
// the subject token, so one server cannot trade in a token issued for another). The credential is never shown to the assistant, lives minutes and is a qm_ token that only the
// tenant API accepts.
func (s *Server) oauthExchange(w http.ResponseWriter, r *http.Request, f url.Values) {
	if !s.resourceAuthed(r) {
		oauthFail(w, http.StatusUnauthorized, "invalid_client", "the exchange needs the MCP server's secret")
		return
	}
	if aud := f.Get("audience"); aud != s.d.OAuth.Issuer || (f.Get("subject_token_type") != "" && f.Get("subject_token_type") != oauth.AccessTokenType) {
		oauthFail(w, http.StatusBadRequest, "invalid_target", "audience must be the API address and subject_token_type an access token")
		return
	}
	resp, err := s.d.Identity.MintOAuthApiToken(r.Context(), &plansv1.MintOAuthApiTokenRequest{AccessToken: f.Get("subject_token"), Resource: f.Get("resource")})
	if err != nil {
		oauthFailFrom(w, err)
		return
	}
	if resp.GetInfo().GetTenant().GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE && !s.d.OAuth.AllowLive {
		oauthFail(w, http.StatusForbidden, "access_denied", "assistants may not act on a live environment on this deployment")
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"access_token": resp.GetToken(), "issued_token_type": oauth.AccessTokenType, "token_type": "Bearer",
		"expires_in": max(1, int64(time.Until(resp.GetExpireTime().AsTime()).Seconds())), "scope": strings.Join(resp.GetInfo().GetScopes(), " ")})
}

// oauthRevoke is RFC 7009. A public client revokes the token it was issued (the grant ends with it); an unknown token is answered like a known one.
func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	f, ok := s.oauthForm(w, r, &s.limits.oauthTokens, oauthTokensPerMinute)
	if !ok {
		return
	}
	if _, err := s.d.Identity.RevokeOAuthToken(r.Context(), &plansv1.RevokeOAuthTokenRequest{Token: f.Get("token"), ClientId: f.Get("client_id")}); err != nil {
		oauthFailFrom(w, err)
		return
	}
	noStore(w)
	writeJSONValue(w, http.StatusOK, map[string]string{})
}

// oauthIntrospect is RFC 7662, for the MCP server only. `resource` is the caller's own address: a token issued for another resource is inactive for it.
func (s *Server) oauthIntrospect(w http.ResponseWriter, r *http.Request) {
	f, ok := s.oauthForm(w, r, &s.limits.oauthResource, oauthResourcePerMinute)
	if !ok {
		return
	}
	noStore(w)
	if !s.resourceAuthed(r) {
		oauthFail(w, http.StatusUnauthorized, "invalid_client", "introspection needs the MCP server's secret")
		return
	}
	info, err := s.d.Identity.IntrospectOAuthToken(r.Context(), &plansv1.IntrospectOAuthTokenRequest{Token: f.Get("token"), Resource: f.Get("resource")})
	if err != nil {
		oauthFailFrom(w, err)
		return
	}
	if !info.GetActive() {
		writeJSONValue(w, http.StatusOK, map[string]bool{"active": false})
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"active": true, "scope": strings.Join(info.GetScopes(), " "), "client_id": info.GetClientId(), "client_name": info.GetClientName(),
		"sub": info.GetUserId(), "aud": info.GetResource(), "exp": info.GetExpireTime().AsTime().Unix(), "token_type": "Bearer", "tenant_id": info.GetTenant().GetId(),
		"environment": envName(info.GetTenant().GetEnvironment()), "member_role": info.GetMemberRole()})
}

func envName(e plansv1.Environment) string {
	if e == plansv1.Environment_ENVIRONMENT_LIVE {
		return tenant.EnvLive
	}
	return tenant.EnvSandbox
}

// ---- the credential on the tenant API ----

// oauthPrincipal accepts a qm_ credential on a tenant route: the person's grant acts as one tenant with the scopes the person's role allows right now, as a custom-role
// key would, so the same permission check as an API key's applies to every route. A live environment is refused when the deployment has switched that off since. The audit
// actor names the person and the client.
func (s *Server) oauthPrincipal(r *http.Request, token string) (auth.Principal, error) {
	if s.d.OAuth == nil {
		return auth.Principal{}, errkey.New(codes.Unauthenticated, "identity.api_key.invalid", "invalid api key")
	}
	info, err := s.d.Identity.ResolveOAuthApiToken(r.Context(), &plansv1.ResolveOAuthApiTokenRequest{Token: token})
	switch status.Code(err) {
	case codes.OK:
	case codes.Unavailable, codes.DeadlineExceeded:
		return auth.Principal{}, errkey.New(codes.Unavailable, "identity.auth.unavailable", "authentication temporarily unavailable")
	default:
		return auth.Principal{}, err
	}
	if !info.GetActive() {
		s.authFailed(r, "oauth")
		return auth.Principal{}, errkey.New(codes.Unauthenticated, "identity.api_key.invalid", "invalid api key")
	}
	if info.GetTenant().GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE && !s.d.OAuth.AllowLive {
		return auth.Principal{}, errkey.New(codes.PermissionDenied, "identity.oauth.live_disabled", "assistants may not act on a live environment on this deployment")
	}
	p, err := principalOf(info.GetTenant())
	if err != nil {
		return p, err
	}
	// shortcut: the audit log has no field for the client, so the actor's e-mail carries it ("ada@example.com via Claude"); a column when the log gets one.
	p.Role, p.Permissions, p.MemberRole = apikey.RoleCustom, info.GetScopes(), info.GetMemberRole()
	p.Actor = audit.Actor{Type: audit.ActorUser, ID: info.GetUserId(), Email: info.GetUserEmail() + " via " + info.GetClientName()}
	return p, nil
}
