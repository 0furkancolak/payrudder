package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"strings"
	"testing"
)

func challengeOf(v string) string {
	s := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

func TestSecretsAreWellFormedAndOnlyTheHashIsKept(t *testing.T) {
	plain, hash, err := NewSecret(AccessPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if !WellFormed(plain, AccessPrefix) || WellFormed(plain, RefreshPrefix) || WellFormed(plain+"x", AccessPrefix) || WellFormed("qo_short", AccessPrefix) {
		t.Fatalf("shape check wrong for %q", plain)
	}
	if string(Hash(plain)) != string(hash) || len(hash) != 32 {
		t.Fatal("hash is not the stored form")
	}
	other, _, _ := NewSecret(AccessPrefix)
	if other == plain {
		t.Fatal("two secrets equal")
	}
}

func TestPKCE(t *testing.T) {
	v := strings.Repeat("a", 43)
	good := challengeOf(v)
	for name, c := range map[string]struct {
		verifier, challenge string
		want                bool
	}{
		"the right verifier":      {v, good, true},
		"another verifier":        {strings.Repeat("b", 43), good, false},
		"too short a verifier":    {"abc", challengeOf("abc"), false},
		"too long a verifier":     {strings.Repeat("a", 129), challengeOf(strings.Repeat("a", 129)), false},
		"a character not allowed": {strings.Repeat("a", 42) + "+", challengeOf(strings.Repeat("a", 42) + "+"), false},
		"a plain challenge":       {v, v, false},
		"no challenge":            {v, "", false},
	} {
		if got := VerifyPKCE(c.verifier, c.challenge); got != c.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
	if ValidChallenge(strings.Repeat("a", 43)) != true || ValidChallenge("short") {
		t.Error("ValidChallenge")
	}
}

func TestResource(t *testing.T) {
	for in, want := range map[string]string{
		"https://mcp.example.com":         "https://mcp.example.com",
		"https://MCP.example.com/":        "https://mcp.example.com",
		"https://mcp.example.com/mcp":     "https://mcp.example.com/mcp",
		"https://mcp.example.com:8443":    "https://mcp.example.com:8443",
		"http://localhost:3000/mcp":       "http://localhost:3000/mcp",
		"http://mcp.example.com":          "",
		"mcp.example.com":                 "",
		"https://mcp.example.com#frag":    "",
		"https://mcp.example.com/?a=b":    "",
		"https://u:p@mcp.example.com/mcp": "",
	} {
		got, ok := Resource(in)
		if ok != (want != "") || got != want {
			t.Errorf("Resource(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestClientIDAndRedirectURIs(t *testing.T) {
	for in, want := range map[string]bool{
		"https://app.example.com/oauth/client.json": true,
		"https://app.example.com":                   false,
		"https://app.example.com/":                  false,
		"http://app.example.com/c.json":             false,
		"https://app.example.com/a/../c.json":       false,
		"https://app.example.com/a/%2e%2e/c.json":   false,
		"https://app.example.com/c.json#x":          false,
		"https://u@app.example.com/c.json":          false,
		"https://localhost/c.json":                  false,
	} {
		if got := ClientIDURL(in); got != want {
			t.Errorf("ClientIDURL(%q) = %v", in, got)
		}
	}
	for in, want := range map[string]bool{
		"https://app.example.com/cb":   true,
		"http://localhost:3000/cb":     true,
		"http://127.0.0.1:8080/cb":     true,
		"http://[::1]:8080/cb":         true,
		"http://app.example.com/cb":    false,
		"myapp://cb":                   false,
		"https://app.example.com/cb#x": false,
		"javascript:alert(1)":          false,
		"//evil.example/cb":            false,
	} {
		if got := RedirectURI(in); got != want {
			t.Errorf("RedirectURI(%q) = %v", in, got)
		}
	}
}

func TestRedirectMatchIsExactExceptTheLoopbackPort(t *testing.T) {
	reg := []string{"https://app.example.com/cb", "http://localhost:3000/callback"}
	for in, want := range map[string]bool{
		"https://app.example.com/cb":      true,
		"https://app.example.com/cb/":     false,
		"https://app.example.com/cb?x=1":  false,
		"https://app.example.com:444/cb":  false,
		"https://evil.example.com/cb":     false,
		"https://app.example.com/cb2":     false,
		"http://localhost:3000/callback":  true,
		"http://localhost:51234/callback": true,
		"http://localhost:3000/other":     false,
		"http://127.0.0.1:3000/callback":  false,
		"https://localhost:3000/callback": false,
		"":                                false,
	} {
		if got := RedirectMatches(reg, in); got != want {
			t.Errorf("RedirectMatches(%q) = %v", in, got)
		}
	}
}

func TestParseClient(t *testing.T) {
	id := "https://app.example.com/client.json"
	ok := `{"client_id":"` + id + `","client_name":"Example\nClient","redirect_uris":["http://localhost:3000/cb"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"]}`
	c, err := ParseClient(id, []byte(ok))
	if err != nil || c.Name != "Example Client" || len(c.RedirectURIs) != 1 {
		t.Fatalf("got %+v, %v", c, err)
	}
	for name, doc := range map[string]string{
		"another client_id":  `{"client_id":"https://evil.example/c.json","client_name":"x","redirect_uris":["https://a.example/cb"]}`,
		"no name":            `{"client_id":"` + id + `","redirect_uris":["https://a.example/cb"]}`,
		"no redirect":        `{"client_id":"` + id + `","client_name":"x"}`,
		"a bad redirect":     `{"client_id":"` + id + `","client_name":"x","redirect_uris":["http://evil.example/cb"]}`,
		"a client secret":    `{"client_id":"` + id + `","client_name":"x","redirect_uris":["https://a.example/cb"],"token_endpoint_auth_method":"client_secret_basic"}`,
		"no code grant":      `{"client_id":"` + id + `","client_name":"x","redirect_uris":["https://a.example/cb"],"grant_types":["implicit"]}`,
		"not JSON":           `<html>`,
		"too many redirects": `{"client_id":"` + id + `","client_name":"x","redirect_uris":["https://a.example/1","https://a.example/2","https://a.example/3","https://a.example/4","https://a.example/5","https://a.example/6","https://a.example/7","https://a.example/8","https://a.example/9","https://a.example/10","https://a.example/11"]}`,
	} {
		if _, err := ParseClient(id, []byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseClient(id, []byte(strings.Repeat(" ", MaxClientDocument+1))); err == nil {
		t.Error("a huge document was accepted")
	}
}

func TestScopes(t *testing.T) {
	perms, unknown := ParseScope("customers:read invoices:read")
	if !slices.Equal(perms, []string{"customers:read", "invoices:read"}) || len(unknown) != 0 {
		t.Fatalf("got %v %v", perms, unknown)
	}
	if perms, _ := ParseScope(""); !slices.Contains(perms, "customers:read") || slices.Contains(perms, "customers:write") || slices.Contains(perms, "audit:read") {
		t.Errorf("the default should be read_only, got %v", perms)
	}
	if perms, _ := ParseScope("ingest_only"); !slices.Equal(perms, []string{"events:write"}) {
		t.Errorf("ingest_only is %v", perms)
	}
	if _, unknown := ParseScope("customers:read root"); !slices.Equal(unknown, []string{"root"}) {
		t.Errorf("unknown = %v", unknown)
	}
	if _, unknown := ParseScope("admin"); len(unknown) != 1 {
		t.Error("admin is not a shorthand")
	}
	both := []string{"customers:read", "customers:write", "events:write", "invoices:read"}
	if got := Allowed("viewer", both); !slices.Equal(got, []string{"customers:read", "invoices:read"}) {
		t.Errorf("a viewer got %v", got)
	}
	if got := Allowed("admin", both); !slices.Equal(got, both) {
		t.Errorf("an admin got %v", got)
	}
}
