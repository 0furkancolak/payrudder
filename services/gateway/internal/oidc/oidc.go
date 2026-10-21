// Package oidc is the gateway's side of "Continue with Google": the OpenID Connect authorization-code flow with PKCE, and the verification of the ID token
// the provider returns. It is written against the standard (discovery, JWKS, RS256) and the provider is a setting, so the same code talks to Google and to
// the in-repo test double (pkg/oidctest) that development and tests use instead of Google.
//
// What it checks on an ID token, in this order: the signature against the provider's published keys (RS256 only: "none" and symmetric algorithms are
// refused), the issuer, the audience (this client), the authorized party when there are several audiences, the expiry and issue time, and the nonce the
// login was started with. The caller still decides what to do with the claims; in particular it must require EmailVerified before it trusts Email.
//
// Secrets (the client secret, codes, tokens) are never logged or placed in an error message.
package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// GoogleIssuer is the issuer of Google's ID tokens and the base of its discovery document.
const GoogleIssuer = "https://accounts.google.com"

// Errors the caller maps to a short reason for the person. Every one means "do not sign in".
var (
	ErrToken     = errors.New("oidc: the ID token was not accepted")
	ErrExchange  = errors.New("oidc: the code could not be exchanged")
	ErrDiscovery = errors.New("oidc: the provider's configuration could not be read")
)

// Config is one provider and this application's registration there.
type Config struct {
	// Issuer is the provider's base address (https://accounts.google.com). Discovery is read from <Issuer>/.well-known/openid-configuration and the
	// document's own "issuer" must equal it.
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is registered at the provider and receives the code: <public URL>/auth/google/callback.
	RedirectURL string
	// HTTP is the client used to reach the provider (default: 10 second timeout).
	HTTP *http.Client
	// Now is the clock (default time.Now), for tests.
	Now func() time.Time
}

// Enabled reports whether the provider is configured: a button is shown only then.
func (c Config) Enabled() bool { return c.ClientID != "" && c.ClientSecret != "" && c.Issuer != "" }

// Client talks to one provider.
type Client struct {
	cfg Config

	mu        sync.Mutex
	disco     *discovery
	discoAt   time.Time
	keys      map[string]*rsa.PublicKey
	keysAt    time.Time
	keysTried time.Time
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

const (
	cacheTTL = time.Hour
	// refetchEvery bounds how often an unknown key id may make the client read the key set again.
	refetchEvery = time.Minute
	clockSkew    = time.Minute
	maxBody      = 1 << 20
)

// New returns a client.
func New(cfg Config) *Client {
	cfg.Issuer = strings.TrimRight(cfg.Issuer, "/")
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{cfg: cfg}
}

// Enabled reports whether the provider is configured.
func (c *Client) Enabled() bool { return c != nil && c.cfg.Enabled() }

func (c *Client) get(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	return c.do(req, into)
}

func (c *Client) do(req *http.Request, into any) error {
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		// The error of net/http names the address and nothing secret, but only its kind is worth keeping.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("oidc: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: provider answered %d", resp.StatusCode)
	}
	return json.Unmarshal(body, into)
}

func (c *Client) discover(ctx context.Context) (*discovery, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disco != nil && c.cfg.Now().Sub(c.discoAt) < cacheTTL {
		return c.disco, nil
	}
	var d discovery
	if err := c.get(ctx, c.cfg.Issuer+"/.well-known/openid-configuration", &d); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDiscovery, err)
	}
	// The document must name the issuer it was fetched for: a different one means the address is not the provider's.
	if strings.TrimRight(d.Issuer, "/") != c.cfg.Issuer || d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, fmt.Errorf("%w: unexpected document", ErrDiscovery)
	}
	c.disco, c.discoAt = &d, c.cfg.Now()
	return c.disco, nil
}

// Request is one started login: what the browser is sent to and what the callback must present again.
type Request struct {
	State    string
	Nonce    string
	Verifier string
	URL      string
}

// Begin makes the state, nonce and PKCE verifier of a new login and the address of the provider's consent page.
func (c *Client) Begin(ctx context.Context) (Request, error) {
	d, err := c.discover(ctx)
	if err != nil {
		return Request{}, err
	}
	state, nonce, verifier := random(16), random(16), random(32)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {c.cfg.ClientID},
		"redirect_uri":          {c.cfg.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		// Always ask which Google account to use: a shared computer must not sign in silently as the last person.
		"prompt": {"select_account"},
	}
	u, err := url.Parse(d.AuthorizationEndpoint)
	if err != nil {
		return Request{}, fmt.Errorf("%w: %w", ErrDiscovery, err)
	}
	u.RawQuery = q.Encode()
	return Request{State: state, Nonce: nonce, Verifier: verifier, URL: u.String()}, nil
}

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the operating system's random source failing is not recoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Claims are the parts of a verified ID token the application uses.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// Exchange trades the authorization code (with the PKCE verifier) for an ID token and verifies it against nonce.
func (c *Client) Exchange(ctx context.Context, code, verifier, nonce string) (Claims, error) {
	d, err := c.discover(ctx)
	if err != nil {
		return Claims{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.cfg.RedirectURL},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Claims{}, ErrExchange
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := c.do(req, &tok); err != nil || tok.IDToken == "" {
		return Claims{}, ErrExchange
	}
	return c.Verify(ctx, tok.IDToken, nonce)
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type audience []string

// UnmarshalJSON accepts "aud" as a string or as a list.
func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// flexBool accepts true and "true": some providers send the latter.
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	switch strings.Trim(string(b), `"`) {
	case "true":
		*f = true
	default:
		*f = false
	}
	return nil
}

type jwtClaims struct {
	Iss           string   `json:"iss"`
	Sub           string   `json:"sub"`
	Aud           audience `json:"aud"`
	Azp           string   `json:"azp"`
	Exp           float64  `json:"exp"`
	Iat           float64  `json:"iat"`
	Nonce         string   `json:"nonce"`
	Email         string   `json:"email"`
	EmailVerified flexBool `json:"email_verified"`
	Name          string   `json:"name"`
}

// Verify checks an ID token (see the package comment) and returns its claims.
func (c *Client) Verify(ctx context.Context, token, nonce string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrToken
	}
	var h jwtHeader
	if err := decodePart(parts[0], &h); err != nil || h.Alg != "RS256" || h.Kid == "" {
		return Claims{}, ErrToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrToken
	}
	key, err := c.key(ctx, h.Kid)
	if err != nil {
		return Claims{}, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) != nil {
		return Claims{}, ErrToken
	}
	var cl jwtClaims
	if err := decodePart(parts[1], &cl); err != nil {
		return Claims{}, ErrToken
	}
	if !c.issuerOK(cl.Iss) {
		return Claims{}, ErrToken
	}
	if !hasAudience(cl, c.cfg.ClientID) {
		return Claims{}, ErrToken
	}
	now := c.cfg.Now()
	if cl.Exp == 0 || now.After(time.Unix(int64(cl.Exp), 0).Add(clockSkew)) {
		return Claims{}, ErrToken
	}
	if cl.Iat != 0 && time.Unix(int64(cl.Iat), 0).After(now.Add(clockSkew)) {
		return Claims{}, ErrToken
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(cl.Nonce), []byte(nonce)) != 1 {
		return Claims{}, ErrToken
	}
	if cl.Sub == "" {
		return Claims{}, ErrToken
	}
	return Claims{Subject: cl.Sub, Email: cl.Email, EmailVerified: bool(cl.EmailVerified), Name: cl.Name}, nil
}

// issuerOK accepts the configured issuer; Google also writes it without the scheme.
func (c *Client) issuerOK(iss string) bool {
	if iss == c.cfg.Issuer {
		return true
	}
	return c.cfg.Issuer == GoogleIssuer && iss == "accounts.google.com"
}

func hasAudience(cl jwtClaims, clientID string) bool {
	found := false
	for _, a := range cl.Aud {
		if subtle.ConstantTimeCompare([]byte(a), []byte(clientID)) == 1 {
			found = true
		}
	}
	// With several audiences the token must say it was issued to this client.
	if found && len(cl.Aud) > 1 {
		return subtle.ConstantTimeCompare([]byte(cl.Azp), []byte(clientID)) == 1
	}
	return found
}

func decodePart(part string, into any) error {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

type jwks struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// key returns the provider's public key with this id. An id the cached set does not know makes the set be read again, at most once a minute, so a
// rotation is followed without a stream of forged ids turning into a stream of requests to the provider.
func (c *Client) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	d, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.cfg.Now()
	if k, ok := c.keys[kid]; ok && now.Sub(c.keysAt) < cacheTTL {
		return k, nil
	}
	if c.keys != nil && now.Sub(c.keysAt) < cacheTTL && now.Sub(c.keysTried) < refetchEvery {
		return nil, ErrToken
	}
	c.keysTried = now
	var set jwks
	if err := c.get(ctx, d.JWKSURI, &set); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDiscovery, err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(n) < 256 || len(e) == 0 || len(e) > 4 {
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	c.keys, c.keysAt = keys, now
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, ErrToken
}
