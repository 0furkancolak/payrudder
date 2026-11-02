package safehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestPublic(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "172.32.0.1": true, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "224.0.0.1": false, "::1": false, "fe80::1": false, "fd00::1": false, "::ffff:127.0.0.1": false, "::ffff:8.8.8.8": true,
	} {
		if got := Public(netip.MustParseAddr(ip)); got != want {
			t.Errorf("Public(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestValidateURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://example.com/hook": true, "http://example.com/hook": false, "https://user:pw@example.com": false, "https:///x": false,
		"https://127.0.0.1/x": false, "https://[::1]/x": false, "https://169.254.169.254/latest": false, "ftp://example.com": false, "not a url": false,
	} {
		if err := ValidateURL(raw, false); (err == nil) != ok {
			t.Errorf("ValidateURL(%q) = %v, want ok=%v", raw, err, ok)
		}
	}
	if err := ValidateURL("http://localhost:8080/x", true); err != nil {
		t.Errorf("allowPrivate: %v", err)
	}
}

func TestValidateLandingURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://shop.example.com/welcome":    true,
		"http://shop.example.com/welcome?a=1": true,
		"https://shop.example.com:8443/x":     true,
		"https://Shop.Example.COM./x#frag":    true,
		"https://münchen.example/x":           true,
		"https://xn--mnchen-3ya.example/":     true,
		// the scheme
		"javascript:alert(1)":         false,
		"data:text/html,<b>x</b>":     false,
		"ftp://shop.example.com":      false,
		"//evil.example":              false,
		"https:evil.example":          false,
		"https://":                    false,
		"https:///evil.example":       false,
		"":                            false,
		"not a url":                   false,
		" https://shop.example.com/":  false,
		"https://shop.example.com/\n": false, // a control character
		"https://shop.exam\nple.com/": false,
		// a name and a password before the host
		"https://shop.example.com@evil.example/":     false,
		"https://user:pw@shop.example.com/":          false,
		"https://shop.example.com:443@evil.example/": false,
		"https://@shop.example.com/":                 false,
		// an IP address, in every spelling a browser accepts
		"https://203.0.113.9/x":            false,
		"https://8.8.8.8/x":                false,
		"https://127.0.0.1/x":              false,
		"https://10.0.0.5/x":               false,
		"https://172.16.0.1/x":             false,
		"https://192.168.1.1/x":            false,
		"https://169.254.169.254/x":        false,
		"https://[::1]/x":                  false,
		"https://[2606:4700:4700::1111]/x": false,
		"https://[::ffff:127.0.0.1]/x":     false,
		"https://2130706433/x":             false,
		"https://0x7f000001/x":             false,
		"https://0x7f.0.0.1/x":             false,
		"https://127.1/x":                  false,
		"https://017700000001/x":           false,
		"https://8.8.8.8./x":               false,
		// names that are no public name
		"https://localhost/x":        false,
		"https://LOCALHOST:3000/x":   false,
		"https://localhost./x":       false,
		"https://app.localhost/x":    false,
		"https://intranet/x":         false,
		"https://printer.local/x":    false,
		"https://db.internal/x":      false,
		"https://router.home.arpa/x": false,
		"https://nas.localdomain/x":  false,
		// a name that merely looks like a number somewhere else is fine
		"https://1password.example.com/x":           true,
		"https://shop.example.com.123abc.example/x": true,
		"https://example.0xg/x":                     true,
	} {
		if err := ValidateLandingURL(raw); (err == nil) != ok {
			t.Errorf("ValidateLandingURL(%q) = %v, want ok=%v", raw, err, ok)
		}
	}
}

func TestClientRefusesPrivateAddressesAndRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/r" {
			http.Redirect(w, r, "/ok", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	do := func(c *http.Client, path string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
		return c.Do(req)
	}
	if _, err := do(NewClient(2*time.Second, false), "/ok"); !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("a loopback server was reachable: %v", err)
	}
	c := NewClient(2*time.Second, true)
	if r, err := do(c, "/ok"); err != nil || r.StatusCode != http.StatusNoContent {
		t.Fatalf("allowPrivate: %v %v", r, err)
	}
	if r, err := do(c, "/r"); err != nil || r.StatusCode != http.StatusFound {
		t.Fatalf("a redirect must be returned, not followed: %v %v", r, err)
	}
}
