package webhooksig

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSignAndVerify(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"id":"evt_1"}`)
	h := Sign(now, body, "new", "old")

	for _, secret := range []string{"new", "old"} { // a rotated secret is accepted while the old signature is still sent
		if err := Verify(h, body, secret, now.Add(time.Minute), 0); err != nil {
			t.Errorf("secret %q: %v", secret, err)
		}
	}
	cases := []struct {
		name   string
		header string
		body   string
		secret string
		at     time.Time
		want   error
	}{
		{"another secret", h, string(body), "other", now, ErrMismatch},
		{"a changed body", h, `{"id":"evt_2"}`, "new", now, ErrMismatch},
		{"too old", h, string(body), "new", now.Add(6 * time.Minute), ErrTooOld},
		{"from the future", h, string(body), "new", now.Add(-6 * time.Minute), ErrTooOld},
		{"no timestamp", "v1=abcd", string(body), "new", now, ErrMalformed},
		{"no signature", "t=1700000000", string(body), "new", now, ErrMalformed},
		{"garbage", "nonsense", string(body), "new", now, ErrMalformed},
	}
	for _, c := range cases {
		if err := Verify(c.header, []byte(c.body), c.secret, c.at, 0); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// A v2 signature covers the method, the path, the query, the time and the body. The v1 signature of an empty body (a GET, a DELETE) covers only the time, so it
// was good for any other empty-bodied request to the same listener for five minutes.
func TestRequestSignatureCoversMethodPathAndQuery(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	list := mustURL(t, "http://notifier:9111/v1/mail-suppressions?tenant_id=a&page_size=10")
	h := SignRequest(now, "GET", list, nil, "secret")
	if !strings.HasPrefix(h, "t=1700000000,v2=") || strings.Contains(h, "v1=") {
		t.Fatalf("header = %q", h)
	}
	ok := func(method, raw string, body []byte, secret string, at time.Time, v1 bool) error {
		return VerifyRequest(h, method, mustURL(t, raw), body, secret, at, 0, v1)
	}
	if err := ok("GET", list.String(), nil, "secret", now.Add(time.Minute), false); err != nil {
		t.Fatalf("the request it was made for: %v", err)
	}
	for name, c := range map[string]struct {
		method, raw, secret string
		body                []byte
		at                  time.Time
		want                error
	}{
		"another method":                      {"DELETE", list.String(), "secret", nil, now, ErrMismatch},
		"another route":                       {"GET", "http://notifier:9111/v1/mail-messages?tenant_id=a&page_size=10", "secret", nil, now, ErrMismatch},
		"a delete of an address (the replay)": {"DELETE", "http://notifier:9111/v1/mail-suppressions/a/ada@example.com", "secret", nil, now, ErrMismatch},
		"another tenant":                      {"GET", "http://notifier:9111/v1/mail-suppressions?tenant_id=b&page_size=10", "secret", nil, now, ErrMismatch},
		"an added parameter":                  {"GET", list.String() + "&after=x", "secret", nil, now, ErrMismatch},
		"a body":                              {"GET", list.String(), "secret", []byte("{}"), now, ErrMismatch},
		"another secret":                      {"GET", list.String(), "other", nil, now, ErrMismatch},
		"too old":                             {"GET", list.String(), "secret", nil, now.Add(6 * time.Minute), ErrTooOld},
	} {
		if err := ok(c.method, c.raw, c.body, c.secret, c.at, false); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	// The host is not part of it: a proxy or a service name in front changes nothing.
	if err := ok("GET", "https://other.example/v1/mail-suppressions?tenant_id=a&page_size=10", nil, "secret", now, false); err != nil {
		t.Errorf("another host: %v", err)
	}
	// Two fields cannot slide into each other: a path ending in what the query started with.
	a := SignRequest(now, "GET", mustURL(t, "http://h/v1/x?y"), nil, "secret")
	if err := VerifyRequest(a, "GET", mustURL(t, "http://h/v1/x%3Fy"), nil, "secret", now, 0, false); !errors.Is(err, ErrMismatch) {
		t.Errorf("an escaped ? in the path is the query: %v", err)
	}
	// Rotation: two secrets in the header, either verifies.
	two := SignRequest(now, "POST", mustURL(t, "http://h/v1/mail-events"), []byte(`{}`), "new", "old")
	for _, s := range []string{"new", "old"} {
		if err := VerifyRequest(two, "POST", mustURL(t, "http://h/v1/mail-events"), []byte(`{}`), s, now, 0, false); err != nil {
			t.Errorf("secret %s: %v", s, err)
		}
	}
}

// v1 is refused unless the receiver says it is in a rollout; a header that carries a v2 is judged by it alone.
func TestVersionOneIsRefusedUnlessAllowed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	u := mustURL(t, "http://h/v1/mail-events")
	body := []byte(`{"type":"delivered"}`)
	v1 := Sign(now, body, "secret")
	if err := VerifyRequest(v1, "POST", u, body, "secret", now, 0, false); !errors.Is(err, ErrV1Refused) {
		t.Errorf("v1 with v1 refused: %v", err)
	}
	if err := VerifyRequest(v1, "POST", u, body, "secret", now, 0, true); err != nil {
		t.Errorf("v1 during a rollout: %v", err)
	}
	if err := VerifyRequest(v1, "POST", u, []byte(`{}`), "secret", now, 0, true); !errors.Is(err, ErrMismatch) {
		t.Errorf("v1 over another body: %v", err)
	}
	both := v1 + ",v2=" + strings.Repeat("0", 64)
	for _, allow := range []bool{false, true} {
		if err := VerifyRequest(both, "POST", u, body, "secret", now, 0, allow); !errors.Is(err, ErrMismatch) {
			t.Errorf("a wrong v2 next to a right v1 (allow v1 %v): %v, want a mismatch: the v2 decides", allow, err)
		}
	}
	// A v2 signed header is no v1: the customers' verifier (Verify) does not accept it.
	if err := Verify(SignRequest(now, "POST", u, body, "secret"), body, "secret", now, 0); !errors.Is(err, ErrMalformed) {
		t.Errorf("Verify of a v2 header: %v", err)
	}
	for _, bad := range []string{"", "nonsense", "t=1700000000", "v2=abcd", "t=x,v2=ab"} {
		if err := VerifyRequest(bad, "POST", u, body, "secret", now, 0, true); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: %v, want malformed", bad, err)
		}
	}
}
