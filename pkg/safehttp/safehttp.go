// Package safehttp makes HTTP requests to addresses a customer chose, without letting them reach the platform's own network: the destination IP is checked
// after DNS resolution, at connect time, so a name that resolves to a private address (or changes its answer between a check and a connection) is refused.
// Redirects are not followed.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrForbiddenAddress: the destination is not a public address.
var ErrForbiddenAddress = errors.New("safehttp: destination address is not public")

var blocked = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16",
	"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"::/128", "::1/128", "64:ff9b::/96", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8",
)

func mustPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, p := range s {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// Public reports whether ip is a routable public address.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// NewClient returns a client with the given total timeout. allowPrivate lifts the address check (development and tests only).
func NewClient(timeout time.Duration, allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second}
	if !allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !Public(ip) {
				return fmt.Errorf("%w: %s", ErrForbiddenAddress, host)
			}
			return nil
		}
	}
	tr := &http.Transport{
		DialContext:           d.DialContext,
		Proxy:                 nil, // a proxy would connect on the customer's behalf and bypass the check
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          50,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ValidateURL checks what can be checked without resolving the name: https only (http only when allowPrivate), a host, no credentials, and a literal IP
// host must itself be public. The connect-time check in NewClient covers names.
func ValidateURL(raw string, allowPrivate bool) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, raw, nil)
	if err != nil {
		return errors.New("not a URL")
	}
	u := req.URL
	switch {
	case u.Scheme != "https" && (!allowPrivate || u.Scheme != "http"):
		return errors.New("must be an https:// URL")
	case u.Hostname() == "":
		return errors.New("has no host")
	case u.User != nil:
		return errors.New("must not carry credentials")
	case len(raw) > 2000:
		return errors.New("is longer than 2000 characters")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !allowPrivate && !Public(ip) {
		return ErrForbiddenAddress
	}
	return nil
}

// ValidateLandingURL checks an address that a visitor's browser is sent to from one of our own: http or https, a host that is a public name (not an IP address in any
// spelling, not "localhost", not a name only a private network resolves), and no user name or password before the host ("https://shop.example@evil.example" shows
// the first host and goes to the second). Nothing is requested from the platform's network, so unlike ValidateURL this is about who the visitor is led to.
func ValidateLandingURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("not a URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("must be an http:// or https:// address")
	}
	if u.User != nil {
		return errors.New("must not carry a user name or password")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	switch {
	case host == "":
		return errors.New("has no host")
	case endsInNumber(host) || strings.Contains(host, ":"):
		return errors.New("must name a host, not an IP address")
	case !strings.Contains(host, ".") || host == "localhost" || hasSuffixIn(host, ".localhost", ".local", ".internal", ".localdomain", ".home.arpa"):
		return errors.New("must be a public host name")
	}
	return nil
}

// endsInNumber is the browsers' test for an IPv4 address in any spelling (127.1, 0x7f.0.0.1, 2130706433): the last label of the host is a number, decimal or 0x hex.
func endsInNumber(host string) bool {
	last := host[strings.LastIndexByte(host, '.')+1:]
	if last == "" {
		return false
	}
	digits := strings.Trim(last, "0123456789")
	if digits == "" {
		return true
	}
	hex, ok := strings.CutPrefix(last, "0x")
	return ok && strings.Trim(hex, "0123456789abcdef") == ""
}

func hasSuffixIn(s string, suffixes ...string) bool {
	for _, x := range suffixes {
		if strings.HasSuffix(s, x) {
			return true
		}
	}
	return false
}
