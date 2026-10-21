// Package oidctest is a small OpenID Connect provider for development and tests: it stands in for Google so the whole "Continue with Google" flow
// (authorization-code with PKCE, state, nonce, signed ID token, discovery and key set) can be exercised without Google, a client registration or the internet.
//
// It is a test double, not a secure provider: it signs tokens with a key generated at start, accepts any e-mail typed on its consent page and keeps its codes in
// memory. It does check what a real provider checks of the client (client id and secret, the exact registered redirect address, PKCE), so a wrong request to it
// fails the way it would at Google. tools/mockoidc serves it on a port of the development stack.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Account is a person the provider can sign in.
type Account struct {
	Email string
	Name  string
	// Subject is the provider's stable id for the person; empty derives one from the e-mail.
	Subject string
	// Unverified makes the provider report email_verified=false.
	Unverified bool
}

// Provider is the test double. The zero value is not usable; use New.
type Provider struct {
	ClientID     string
	ClientSecret string
	// RedirectURIs are the addresses registered for the client; the authorization request must name one of them exactly.
	RedirectURIs []string

	// BrowserURL is the address the person's browser reaches this provider at, when it differs from the issuer address the gateway uses (a container
	// network). The consent page form posts back to it.
	BrowserURL string

	// Account, when set, signs in without asking: the consent page is skipped and this person is used (tests). Otherwise the page asks for an e-mail.
	Account *Account
	// Tamper, when set, may change the ID token's claims just before signing (tests: wrong audience, nonce, expiry...).
	Tamper func(claims map[string]any)
	// SignWith, when set, signs the ID token with this key instead of the published one (tests: a forged signature).
	SignWith *rsa.PrivateKey

	mu     sync.Mutex
	issuer string
	key    *rsa.PrivateKey
	codes  map[string]*grant
	now    func() time.Time
}

type grant struct {
	account     Account
	nonce       string
	challenge   string
	redirectURI string
	expires     time.Time
}

// New returns a provider for one client. Call SetIssuer with the address it is served at before the first request.
func New(clientID, clientSecret string, redirectURIs ...string) *Provider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return &Provider{ClientID: clientID, ClientSecret: clientSecret, RedirectURIs: redirectURIs, key: key, codes: map[string]*grant{}, now: time.Now}
}

// SetIssuer sets the provider's own address (no trailing slash), which it reports as the issuer.
func (p *Provider) SetIssuer(issuer string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.issuer = strings.TrimRight(issuer, "/")
}

// Key is the private key the ID tokens are signed with (tests that forge a token need a different one, so they make their own).
func (p *Provider) Key() *rsa.PrivateKey { return p.key }

// ServeHTTP implements http.Handler.
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/.well-known/openid-configuration":
		p.discovery(w)
	case r.URL.Path == "/jwks":
		p.jwks(w)
	case r.URL.Path == "/authorize":
		p.authorize(w, r)
	case r.URL.Path == "/authorize/approve" && r.Method == http.MethodPost:
		p.approve(w, r)
	case r.URL.Path == "/token" && r.Method == http.MethodPost:
		p.token(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *Provider) discovery(w http.ResponseWriter) {
	p.mu.Lock()
	iss := p.issuer
	p.mu.Unlock()
	// The consent page is opened by the person's browser, the token and key endpoints by the gateway: in a container network they are different addresses.
	browser := iss
	if p.BrowserURL != "" {
		browser = strings.TrimRight(p.BrowserURL, "/")
	}
	writeJSON(w, map[string]any{
		"issuer": iss, "authorization_endpoint": browser + "/authorize", "token_endpoint": iss + "/token", "jwks_uri": iss + "/jwks",
		"response_types_supported": []string{"code"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "subject_types_supported": []string{"public"},
		"code_challenge_methods_supported": []string{"S256"},
	})
}

func (p *Provider) jwks(w http.ResponseWriter) {
	pub := p.key.PublicKey
	writeJSON(w, map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "oidctest-1",
		"n": b64(pub.N.Bytes()), "e": b64([]byte{byte(pub.E >> 16), byte(pub.E >> 8), byte(pub.E)}),
	}}})
}

// authorize checks the request the way a provider does and either signs the configured account in or shows a one-field consent page.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	if q.Get("client_id") != p.ClientID || !p.registered(redirect) {
		http.Error(w, "unknown client or redirect address", http.StatusBadRequest)
		return
	}
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" {
		p.back(w, r, redirect, url.Values{"error": {"invalid_request"}, "state": {q.Get("state")}})
		return
	}
	if p.Account != nil {
		p.grantTo(w, r, *p.Account, q)
		return
	}
	var f strings.Builder
	for _, k := range []string{"redirect_uri", "state", "nonce", "code_challenge"} {
		fmt.Fprintf(&f, `<input type="hidden" name="%s" value="%s">`, k, html.EscapeString(q.Get(k)))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Mock sign-in</title><body style="font-family:system-ui;max-width:420px;margin:48px auto">
<h1>Mock identity provider</h1><p>A stand-in for Google in development. Type any address; nothing is checked.</p>
<form method="post" action="/authorize/approve">%s
<p><label>E-mail <input name="email" type="email" required value="%s" style="width:100%%"></label></p>
<p><label>Name <input name="name" value="Mock Person" style="width:100%%"></label></p>
<p><label><input name="unverified" type="checkbox" value="1"> report the address as not verified</label></p>
<p><button>Continue</button> <a href="%s?error=access_denied&state=%s">Cancel</a></p></form>`,
		f.String(), html.EscapeString(q.Get("login_hint")), html.EscapeString(redirect), url.QueryEscape(q.Get("state")))
}

func (p *Provider) approve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !p.registered(r.PostForm.Get("redirect_uri")) {
		http.Error(w, "unknown redirect address", http.StatusBadRequest)
		return
	}
	a := Account{Email: strings.TrimSpace(r.PostForm.Get("email")), Name: r.PostForm.Get("name"), Unverified: r.PostForm.Get("unverified") != ""}
	p.grantTo(w, r, a, r.PostForm)
}

func (p *Provider) grantTo(w http.ResponseWriter, r *http.Request, a Account, q interface{ Get(string) string }) {
	code := rnd(24)
	p.mu.Lock()
	p.codes[code] = &grant{account: a, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri"), expires: p.now().Add(2 * time.Minute)}
	p.mu.Unlock()
	p.back(w, r, q.Get("redirect_uri"), url.Values{"code": {code}, "state": {q.Get("state")}})
}

func (p *Provider) registered(u string) bool {
	for _, x := range p.RedirectURIs {
		if x == u {
			return true
		}
	}
	return false
}

func (p *Provider) back(w http.ResponseWriter, r *http.Request, to string, q url.Values) {
	u, err := url.Parse(to)
	if err != nil {
		http.Error(w, "bad redirect", http.StatusBadRequest)
		return
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// token exchanges a code: client authentication, single use, exact redirect address, PKCE.
func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request")
		return
	}
	f := r.PostForm
	if f.Get("grant_type") != "authorization_code" {
		tokenError(w, "unsupported_grant_type")
		return
	}
	if f.Get("client_id") != p.ClientID || subtle.ConstantTimeCompare([]byte(f.Get("client_secret")), []byte(p.ClientSecret)) != 1 {
		tokenError(w, "invalid_client")
		return
	}
	p.mu.Lock()
	g, ok := p.codes[f.Get("code")]
	delete(p.codes, f.Get("code"))
	iss := p.issuer
	p.mu.Unlock()
	if !ok || p.now().After(g.expires) || g.redirectURI != f.Get("redirect_uri") {
		tokenError(w, "invalid_grant")
		return
	}
	sum := sha256.Sum256([]byte(f.Get("code_verifier")))
	if subtle.ConstantTimeCompare([]byte(b64(sum[:])), []byte(g.challenge)) != 1 {
		tokenError(w, "invalid_grant")
		return
	}
	sub := g.account.Subject
	if sub == "" {
		s := sha256.Sum256([]byte(strings.ToLower(g.account.Email)))
		sub = "mock-" + b64(s[:12])
	}
	now := p.now()
	claims := map[string]any{
		"iss": iss, "sub": sub, "aud": p.ClientID, "azp": p.ClientID, "iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(), "nonce": g.nonce,
		"email": g.account.Email, "email_verified": !g.account.Unverified, "name": g.account.Name,
	}
	if p.Tamper != nil {
		p.Tamper(claims)
	}
	key := p.key
	if p.SignWith != nil {
		key = p.SignWith
	}
	idToken, err := sign(key, claims)
	if err != nil {
		tokenError(w, "server_error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"access_token": rnd(16), "token_type": "Bearer", "expires_in": 3600, "id_token": idToken})
}

func sign(key *rsa.PrivateKey, claims map[string]any) (string, error) {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "oidctest-1"})
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signing := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + b64(sig), nil
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func rnd(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b64(b)
}
