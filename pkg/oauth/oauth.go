// Package oauth holds the rules of Payrudder's OAuth 2.1 authorization server that do not need a database or a network (docs/plans/20-mcp.md, "OAuth design"): the shape and
// hash of codes and tokens, PKCE, the shape of a resource identifier, a client_id URL and a redirect URI, the Client ID Metadata Document, and the scopes. The gateway
// (HTTP, client documents) and plans-svc (grants, codes, tokens) both use it, so the two cannot disagree about what is valid.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/apikey"
)

// Prefixes of what the server issues (an API key starts with qk_, a console session with qs_).
const (
	AccessPrefix  = "qo_" // the access token the assistant presents to the MCP server
	RefreshPrefix = "qr_" // the refresh token: used once, then replaced
	APIPrefix     = "qm_" // the credential for the API audience that the MCP server gets in exchange for an access token; never shown to the assistant
)

// Lifetimes.
const (
	CodeTTL    = time.Minute
	AccessTTL  = time.Hour
	RefreshTTL = 30 * 24 * time.Hour
	APITTL     = 5 * time.Minute
)

// GrantTypeExchange is RFC 8693 token exchange, the grant the MCP server uses to get the API credential.
const GrantTypeExchange = "urn:ietf:params:oauth:grant-type:token-exchange"

// AccessTokenType is the RFC 8693 identifier of an access token.
const AccessTokenType = "urn:ietf:params:oauth:token-type:access_token" //nolint:gosec // an identifier from RFC 8693, not a credential

// NewSecret returns a random value of 256 bits as text and its SHA-256, the only form stored. A prefix goes in front of a token; a code has none.
func NewSecret(prefix string) (plain string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, err
	}
	plain = prefix + base64.RawURLEncoding.EncodeToString(raw)
	return plain, Hash(plain), nil
}

// Hash is what is stored for a code or a token. A slow hash is not needed for 256 random bits.
func Hash(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}

// WellFormed says whether s is shaped like something NewSecret made with prefix: it is checked before the database is asked.
func WellFormed(s, prefix string) bool {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok || len(rest) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(rest)
	return err == nil
}

// ValidChallenge says whether c can be an S256 code challenge: the unpadded base64url of a SHA-256, 43 characters.
func ValidChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil && len(b) == sha256.Size
}

// VerifyPKCE says whether the verifier is the one the S256 challenge was made from. The verifier is 43 to 128 characters of the unreserved set (RFC 7636 4.1).
func VerifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 || !ValidChallenge(challenge) {
		return false
	}
	for _, r := range verifier {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '.' && r != '_' && r != '~' {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// Resource reads a resource indicator (RFC 8707): an absolute URI with no fragment, https, or http for a loopback host (development). The canonical form has no trailing
// slash, which is how the MCP specification asks servers to spell it. A query is refused: a resource names a server, not a request.
func Resource(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery || u.User != nil || u.Opaque != "" {
		return "", false
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback(u.Hostname())) {
		return "", false
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/"), true
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// ClientIDURL says whether s can be the client_id of a Client ID Metadata Document: https, a host, a path other than "/", no fragment, no credentials, no dot segments.
func ClientIDURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || len(s) > 2048 || u.Path == "" || u.Path == "/" {
		return false
	}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if seg == "." || seg == ".." || strings.EqualFold(seg, "%2e") || strings.EqualFold(seg, "%2e%2e") {
			return false
		}
	}
	return !loopback(u.Hostname())
}

// RedirectURI says whether s may be a redirect URI at all: https, or http to a loopback host (a desktop client listens there), no fragment, no credentials.
func RedirectURI(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Fragment != "" || u.User != nil || len(s) > 2048 || u.Opaque != "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname())
}

// RedirectMatches says whether got is one of the registered URIs: an exact string match. The one exception is RFC 8252 7.3: a loopback redirect (http, localhost or a
// loopback address) may use a different port from the registered one, because a desktop client takes whatever port is free; scheme, host and path still have to be equal.
func RedirectMatches(registered []string, got string) bool {
	if !RedirectURI(got) {
		return false
	}
	if slices.Contains(registered, got) {
		return true
	}
	g, _ := url.Parse(got)
	if g.Scheme != "http" {
		return false
	}
	for _, r := range registered {
		u, err := url.Parse(r)
		if err == nil && u.Scheme == "http" && loopback(u.Hostname()) && u.Hostname() == g.Hostname() && u.EscapedPath() == g.EscapedPath() && u.RawQuery == g.RawQuery {
			return true
		}
	}
	return false
}

// IsLoopbackRedirect says whether the redirect goes to the person's own machine; the consent page shows a warning for it (a Client ID Metadata Document cannot stop
// another program on the machine from listening on the port).
func IsLoopbackRedirect(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "http" && loopback(u.Hostname())
}

// Client is what the server knows of a client after it has read its Client ID Metadata Document.
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
}

// MaxClientDocument bounds the document that is read (a metadata document is a few hundred bytes).
const MaxClientDocument = 32 << 10

// ParseClient reads and validates a Client ID Metadata Document fetched from clientID: the client_id inside must equal the URL it was fetched from exactly, the name and at
// least one redirect URI are required and every redirect URI must be acceptable, and the client must be a public one that uses the authorization code flow (no client secret
// is ever checked, so a document that asks for another authentication method is refused rather than silently weakened).
func ParseClient(clientID string, doc []byte) (Client, error) {
	if len(doc) > MaxClientDocument {
		return Client{}, errors.New("the client document is too large")
	}
	var d struct {
		ClientID     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		Response     []string `json:"response_types"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return Client{}, errors.New("the client document is not JSON")
	}
	switch {
	case d.ClientID != clientID:
		return Client{}, errors.New("the client_id in the document is not the address it was fetched from")
	case strings.TrimSpace(d.ClientName) == "" || len(d.ClientName) > 200:
		return Client{}, errors.New("the client document needs a client_name of at most 200 characters")
	case len(d.RedirectURIs) == 0 || len(d.RedirectURIs) > 10:
		return Client{}, errors.New("the client document needs between 1 and 10 redirect_uris")
	case d.AuthMethod != "" && d.AuthMethod != "none":
		return Client{}, errors.New("only token_endpoint_auth_method none is supported")
	case len(d.GrantTypes) > 0 && !slices.Contains(d.GrantTypes, "authorization_code"):
		return Client{}, errors.New("the client must use the authorization_code grant")
	case len(d.Response) > 0 && !slices.Contains(d.Response, "code"):
		return Client{}, errors.New("the client must use the code response type")
	}
	for _, r := range d.RedirectURIs {
		if !RedirectURI(r) {
			return Client{}, fmt.Errorf("redirect URI %q must be https, or http to localhost", r)
		}
	}
	return Client{ID: clientID, Name: oneLine(d.ClientName), RedirectURIs: d.RedirectURIs}, nil
}

// oneLine keeps a client's name from breaking the consent page's layout or smuggling a line break into a log.
func oneLine(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, s))
}

// Scope shorthands: a role stands for its permissions (pkg/apikey). The admin role is not offered as one; an assistant that needs everything asks for the permissions.
var shorthands = []string{apikey.RoleReadOnly, apikey.RoleBilling, apikey.RoleIngestOnly}

// Scopes lists the scopes the server supports, in the order the console shows them: the permissions of an API key, then the shorthands.
func Scopes() []string { return slices.Concat(apikey.Permissions, shorthands) }

// ParseScope turns the scope parameter (space separated) into the permissions it stands for, in the vocabulary's order. An empty parameter is the read_only shorthand: an
// assistant that does not say what it needs gets the least that is useful. A name that is not a scope is returned in unknown (the caller refuses the request).
func ParseScope(s string) (perms, unknown []string) {
	names := strings.Fields(s)
	if len(names) == 0 {
		names = []string{apikey.RoleReadOnly}
	}
	for _, n := range names {
		switch {
		case apikey.ValidPermission(n):
			perms = append(perms, n)
		case slices.Contains(shorthands, n):
			p, _ := apikey.RolePermissions(n)
			perms = append(perms, p...)
		default:
			unknown = append(unknown, n)
		}
	}
	_, perms, _ = apikey.Resolve(apikey.RoleCustom, perms)
	return perms, unknown
}

// Allowed is the intersection of the permissions asked for with what the person's role in the account allows: a viewer never gets a write scope, whatever the client
// asks. It is the rule the gateway applies to a session acting as a tenant (a viewer may only read).
func Allowed(memberRole string, perms []string) []string {
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		if memberRole == "viewer" && !strings.HasSuffix(p, ":read") {
			continue
		}
		out = append(out, p)
	}
	return out
}
