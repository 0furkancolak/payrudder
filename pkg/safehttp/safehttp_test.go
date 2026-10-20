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
