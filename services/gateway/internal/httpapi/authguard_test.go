package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/0furkancolak/payrudder/services/gateway/internal/auth"
)

// --- IPv6: one subscriber gets a /64 and can use any address in it, so the per-address limits count the /64 ---

func TestLimitKeyGroupsAnIPv6NetworkByItsSlash64(t *testing.T) {
	same := [][2]string{
		{"2001:db8:1:2::1", "2001:db8:1:2:ffff:ffff:ffff:ffff"},
		{"2001:DB8:1:2::1", "2001:db8:1:2:0:0:0:9"},
		{"203.0.113.9", "::ffff:203.0.113.9"}, // an IPv4 address that arrived on a dual-stack socket
	}
	for _, c := range same {
		if limitKey(c[0]) != limitKey(c[1]) {
			t.Errorf("%s and %s count separately: %q %q", c[0], c[1], limitKey(c[0]), limitKey(c[1]))
		}
	}
	differ := [][2]string{
		{"2001:db8:1:2::1", "2001:db8:1:3::1"}, // the next /64
		{"203.0.113.9", "203.0.113.10"},        // IPv4 stays per address
		{"2001:db8:1:2::1", "203.0.113.9"},
	}
	for _, c := range differ {
		if limitKey(c[0]) == limitKey(c[1]) {
			t.Errorf("%s and %s share a bucket: %q", c[0], c[1], limitKey(c[0]))
		}
	}
	if limitKey("not an address") != "not an address" || limitKey("") != "" {
		t.Error("something that is no address is counted as it is")
	}
	if got := limitKey("fe80::1%eth0"); got != "fe80::/64" {
		t.Errorf("a zone is no part of the key: %q", got)
	}
}

// Two addresses of one /64 share the limit of every route a visitor can hammer; a neighbour in the next /64 does not.
func TestPublicLimitsCountAnIPv6SlashSixtyFourOnce(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		limit            int
	}{
		{"sign in", "/auth/signin", `{"email":"ada@example.com","password":"nope"}`, signInsPerMinute},
		{"sign up", "/auth/signup", `{"email":"new@example.com","password":"long enough password"}`, signUpsPerMinute},
		{"code request", "/auth/otp/request", `{"email":"x@example.com"}`, otpRequestsPerMinute},
		{"code verify", "/auth/otp/verify", `{"email":"x@example.com","code":"000000"}`, otpVerifiesPerMinute},
		{"second factor", "/auth/2fa/verify", `{"challenge":"` + goodChallenge + `","code":"000000"}`, twoFactorPerMinute},
	} {
		p := newProduct(t, nil, func(d *Deps) { d.TrustForwardedFor = true })
		for i := range tc.limit {
			// The addresses alternate inside one /64: neither alone reaches the limit.
			if resp, m := p.callFrom(t, fmt.Sprintf("2001:db8:aaaa:1::%x", i+1), "POST", tc.path, "", "", tc.body); resp.StatusCode == 429 {
				t.Fatalf("%s: attempt %d was refused early: %v", tc.name, i+1, m)
			}
		}
		resp, m := p.callFrom(t, "2001:db8:aaaa:1:ffff::7", "POST", tc.path, "", "", tc.body)
		if resp.StatusCode != 429 || errKeyOf(m) != "identity.auth.rate_limited" {
			t.Errorf("%s: a new address of the same /64 after %d tries: %d %v, want 429", tc.name, tc.limit, resp.StatusCode, m)
		}
		if resp, _ := p.callFrom(t, "2001:db8:aaaa:2::1", "POST", tc.path, "", "", tc.body); resp.StatusCode == 429 {
			t.Errorf("%s: the next /64 shares the limit", tc.name)
		}
	}
}

func TestHostedLimitsCountAnIPv6SlashSixtyFourOnce(t *testing.T) {
	s := &Server{d: Deps{TrustForwardedFor: true}, now: time.Now}
	from := func(addr string) *http.Request {
		r := httptest.NewRequest("GET", "/i/token", nil)
		r.RemoteAddr = "10.0.0.5:1234"
		r.Header.Set("X-Forwarded-For", addr)
		return r
	}
	for i := range hostedPerMinute {
		if !s.hostedAllowed(httptest.NewRecorder(), from(fmt.Sprintf("2001:db8:bbbb:1::%x", i+1))) {
			t.Fatalf("page %d was refused early", i+1)
		}
	}
	rec := httptest.NewRecorder()
	if s.hostedAllowed(rec, from("2001:db8:bbbb:1:abcd::1")) || rec.Code != 429 {
		t.Errorf("a new address of the same /64 after %d pages: %d", hostedPerMinute, rec.Code)
	}
	if !s.hostedAllowed(httptest.NewRecorder(), from("2001:db8:bbbb:2::1")) {
		t.Error("the next /64 shares the page limit")
	}
	// The stricter limit on the POSTs that start a payment is per /64 too.
	for i := range hostedPaysPerMinute {
		if !s.hostedPayAllowed(httptest.NewRecorder(), from(fmt.Sprintf("2001:db8:cccc:1::%x", i+1))) {
			t.Fatalf("payment %d was refused early", i+1)
		}
	}
	if s.hostedPayAllowed(httptest.NewRecorder(), from("2001:db8:cccc:1:abcd::1")) {
		t.Errorf("a new address of the same /64 after %d payments was allowed", hostedPaysPerMinute)
	}
	// What is recorded about the visitor (an acceptance's address, the audit log) is the address itself, not the network.
	if got := s.clientAddr(from("2001:db8:bbbb:1::5")); got != "2001:db8:bbbb:1::5" {
		t.Errorf("clientAddr = %s", got)
	}
}

// --- failed authentications: counted per address, and the address is refused before any backend call once it has failed too often ---

// countingAuth counts the calls that reach the key lookup.
type countingAuth struct {
	mu    sync.Mutex
	calls int
}

func (c *countingAuth) Authenticate(ctx context.Context, key string) (auth.Principal, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return fakeAuth{}.Authenticate(ctx, key)
}

func (c *countingAuth) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// authFailureCounts reads the counter the way a scrape would: reason -> count.
func authFailureCounts(t *testing.T, r *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok && m.Name == "gateway_auth_failures" {
				for _, dp := range sum.DataPoints {
					reason, _ := dp.Attributes.Value("reason")
					out[reason.AsString()] += dp.Value
				}
			}
		}
	}
	return out
}

func newFailureCounter(t *testing.T) (metric.Int64Counter, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	c, err := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test").Int64Counter("gateway_auth_failures")
	if err != nil {
		t.Fatal(err)
	}
	return c, reader
}

func TestAnAddressThatFailsToAuthenticateTooOftenIsRefusedBeforeAnyBackendCall(t *testing.T) {
	counter, reader := newFailureCounter(t)
	keys := &countingAuth{}
	p := newProduct(t, nil, func(d *Deps) { d.TrustForwardedFor = true; d.Auth = keys; d.AuthFailures = counter })
	const attacker, neighbour, elsewhere = "2001:db8:dddd:1::1", "2001:db8:dddd:1:ffff::2", "198.51.100.7"

	// Random session tokens and random API keys: every one is a failure, whichever kind.
	for i := range authFailuresPerMinute {
		var resp *http.Response
		if i%2 == 0 {
			resp, _ = p.callFrom(t, attacker, "GET", "/app/v1/apps", fmt.Sprintf("qs_guess%d", i), "", "")
		} else {
			resp, _ = p.callFrom(t, attacker, "GET", "/v1/customers", fmt.Sprintf("qk_live_guess%d", i), "", "")
		}
		if resp.StatusCode != 401 {
			t.Fatalf("failure %d: %d, want 401", i+1, resp.StatusCode)
		}
	}
	sessionsAsked, keysAsked := p.ident.getSessions, keys.count()
	if sessionsAsked != authFailuresPerMinute/2 || keysAsked != authFailuresPerMinute/2 {
		t.Fatalf("backend calls so far: %d sessions, %d keys", sessionsAsked, keysAsked)
	}

	// The next request of the address, or of its /64, is refused with 429 and a Retry-After, before the session or key is looked up: a new random token, an old one, a
	// valid one (the price of sharing an address with whoever is guessing), and the other kind of route.
	for _, tc := range []struct{ name, from, method, path, token string }{
		{"a new random session", attacker, "GET", "/app/v1/apps", "qs_brandnew"},
		{"a random key", neighbour, "GET", "/v1/customers", "qk_live_brandnew"},
		{"a valid session", neighbour, "GET", "/app/v1/apps", tokAda},
		{"a valid key", attacker, "GET", "/v1/customers", "live-key"},
		{"an operator route", attacker, "POST", "/admin/v1/accounts", "qs_brandnew"},
	} {
		resp, m := p.callFrom(t, tc.from, tc.method, tc.path, tc.token, "", `{"name":"Acme"}`)
		if resp.StatusCode != 429 || errKeyOf(m) != "identity.auth.rate_limited" || resp.Header.Get("Retry-After") == "" {
			t.Errorf("%s: %d %v, want 429 identity.auth.rate_limited with Retry-After", tc.name, resp.StatusCode, m)
		}
	}
	if p.ident.getSessions != sessionsAsked || keys.count() != keysAsked {
		t.Errorf("a refused request reached the backend: sessions %d -> %d, keys %d -> %d", sessionsAsked, p.ident.getSessions, keysAsked, keys.count())
	}
	// Somebody else is not affected, and a request with no credential is no authentication at all.
	if resp, _ := p.callFrom(t, elsewhere, "GET", "/app/v1/apps", tokAda, "", ""); resp.StatusCode != 200 {
		t.Errorf("another address: %d", resp.StatusCode)
	}
	if resp, _ := p.callFrom(t, attacker, "GET", "/auth/methods", "", "", ""); resp.StatusCode != 200 {
		t.Errorf("a public route: %d", resp.StatusCode)
	}
	// The machine token is checked in memory and is never locked out by whoever shares its address.
	if resp, _ := p.callFrom(t, attacker, "POST", "/admin/v1/accounts", operatorToken, "", `{"name":"Acme"}`); resp.StatusCode != 201 {
		t.Errorf("the operator token from a throttled address: %d", resp.StatusCode)
	}
	// The counter says what happened: the failures by what was wrong, and the refusals.
	got := authFailureCounts(t, reader)
	if got["session"] != authFailuresPerMinute/2 || got["throttled"] != 5 {
		t.Errorf("gateway_auth_failures by reason = %v, want session=%d throttled=5", got, authFailuresPerMinute/2)
	}
}

// Failures are forgotten after a minute, and a genuine request is never counted.
func TestSuccessesDoNotCountAndFailuresAreForgotten(t *testing.T) {
	p := newProduct(t, nil, func(d *Deps) { d.TrustForwardedFor = true })
	for range 3 * authFailuresPerMinute {
		if resp, _ := p.callFrom(t, "198.51.100.9", "GET", "/app/v1/apps", tokAda, "", ""); resp.StatusCode != 200 {
			t.Fatalf("a valid session was refused: %d", resp.StatusCode)
		}
	}
	s := &Server{}
	now := time.Now()
	for range authFailuresPerMinute {
		s.limits.authFailures.allowN("k", now, authFailuresPerMinute)
	}
	if !s.limits.authFailures.over("k", now, authFailuresPerMinute) {
		t.Fatal("the address is not over after its failures")
	}
	if s.limits.authFailures.over("k", now.Add(61*time.Second), authFailuresPerMinute) {
		t.Error("the address is still over after a minute")
	}
	if s.limits.authFailures.over("other", now, authFailuresPerMinute) {
		t.Error("asking about an address counted it")
	}
}

// An unknown session token is asked about once in a while; the same token again is refused from memory. It is not a new guess, so it does not count against the
// address: a client that keeps using an expired session must not get everybody behind its address throttled.
func TestAnUnknownSessionTokenIsAskedOnce(t *testing.T) {
	p := newProduct(t, nil, func(d *Deps) { d.TrustForwardedFor = true })
	for range 5 {
		if resp, _ := p.callFrom(t, "198.51.100.20", "GET", "/app/v1/apps", "qs_unknown", "", ""); resp.StatusCode != 401 {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	if p.ident.getSessions != 1 {
		t.Errorf("plans-svc was asked %d times about one unknown token, want 1", p.ident.getSessions)
	}
	for range 3 * authFailuresPerMinute {
		p.callFrom(t, "198.51.100.20", "GET", "/app/v1/apps", "qs_unknown", "", "")
	}
	if resp, _ := p.callFrom(t, "198.51.100.20", "GET", "/app/v1/apps", tokAda, "", ""); resp.StatusCode != 200 {
		t.Errorf("the same unknown token over and over throttled its address: %d", resp.StatusCode)
	}
	// A real session is asked about every time (sign-out and revocation take effect at once).
	for range 3 {
		p.callFrom(t, "198.51.100.21", "GET", "/app/v1/apps", tokAda, "", "")
	}
	if p.ident.getSessions != 5 {
		t.Errorf("GetSession calls = %d, want 5 (one unknown token, four real ones)", p.ident.getSessions)
	}
	// Signing out ends the session at once, and the ended token is then just an unknown one.
	if resp, _ := p.callFrom(t, "198.51.100.21", "POST", "/auth/signout", tokBob, "", ""); resp.StatusCode != 204 {
		t.Fatalf("sign out: %d", resp.StatusCode)
	}
	if resp, _ := p.callFrom(t, "198.51.100.21", "GET", "/app/v1/apps", tokBob, "", ""); resp.StatusCode != 401 {
		t.Errorf("a signed-out session: %d", resp.StatusCode)
	}
}

// auth refuses a key it has just refused with ErrRepeated, which is the same guess again and not counted against the address.
type repeatingAuth struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (a *repeatingAuth) Authenticate(ctx context.Context, key string) (auth.Principal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, err := (fakeAuth{}).Authenticate(ctx, key); err == nil {
		return p, nil
	}
	if a.seen[key] {
		return auth.Principal{}, auth.ErrRepeated
	}
	a.seen[key] = true
	return auth.Principal{}, auth.ErrUnauthenticated
}

func TestARevokedKeyUsedOverAndOverDoesNotThrottleItsAddress(t *testing.T) {
	p := newProduct(t, nil, func(d *Deps) { d.TrustForwardedFor = true; d.Auth = &repeatingAuth{seen: map[string]bool{}} })
	for range 5 * authFailuresPerMinute {
		if resp, _ := p.callFrom(t, "198.51.100.30", "GET", "/v1/customers", "qk_live_revoked", "", ""); resp.StatusCode != 401 {
			t.Fatalf("a refused key answered %d, want 401 every time", resp.StatusCode)
		}
	}
	if resp, _ := p.callFrom(t, "198.51.100.30", "GET", "/v1/customers", "live-key", "", ""); resp.StatusCode != 200 {
		t.Errorf("a valid key from that address: %d", resp.StatusCode)
	}
	// New keys are new guesses.
	for i := range authFailuresPerMinute {
		p.callFrom(t, "198.51.100.31", "GET", "/v1/customers", fmt.Sprintf("qk_live_guess%d", i), "", "")
	}
	if resp, _ := p.callFrom(t, "198.51.100.31", "GET", "/v1/customers", "live-key", "", ""); resp.StatusCode != 429 {
		t.Errorf("after %d different bad keys: %d, want 429", authFailuresPerMinute, resp.StatusCode)
	}
}
