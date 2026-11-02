package totp

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// The test values of RFC 6238 appendix B for SHA-1: the secret is the ASCII string 12345678901234567890 and the codes have eight digits. Six digits are the last six.
func TestRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		unix int64
		code string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	} {
		if got := code(secret, v.unix/Period, 8); got != v.code {
			t.Errorf("T=%d: got %s, want %s", v.unix, got, v.code)
		}
		if got := Code(secret, v.unix/Period); got != v.code[2:] {
			t.Errorf("T=%d: six digits got %s, want %s", v.unix, got, v.code[2:])
		}
	}
}

func TestMatchAcceptsOneStepEitherSideAndNeverAReplay(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	cur := Step(now)
	for _, c := range []struct {
		name  string
		step  int64
		after int64
		ok    bool
	}{
		{"this step", cur, 0, true},
		{"the step before (a slow clock)", cur - 1, 0, true},
		{"the step after (a fast clock)", cur + 1, 0, true},
		{"two steps before", cur - 2, 0, false},
		{"two steps after", cur + 2, 0, false},
		{"a step already used", cur, cur, false},
		{"an earlier step than the last used", cur - 1, cur, false},
	} {
		step, ok := Match(secret, Code(secret, c.step), now, c.after)
		if ok != c.ok || (ok && step != c.step) {
			t.Errorf("%s: step %d ok %v, want ok %v", c.name, step, ok, c.ok)
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", Code(secret, cur) + "0"} {
		if _, ok := Match(secret, bad, now, 0); ok {
			t.Errorf("%q matched", bad)
		}
	}
}

func TestURIIsWhatAnAuthenticatorReads(t *testing.T) {
	secret := []byte("12345678901234567890")
	u, err := url.Parse(URI("Payrudder", "alice+test@example.com", secret))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/Payrudder:alice+test@example.com" {
		t.Errorf("scheme/host/path = %s %s %s", u.Scheme, u.Host, u.Path)
	}
	q := u.Query()
	if q.Get("secret") != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" || q.Get("issuer") != "Payrudder" || q.Get("digits") != "6" || q.Get("period") != "30" || q.Get("algorithm") != "SHA1" {
		t.Errorf("query = %v", q)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != recoveryLen+1 || c[recoveryGroupBy] != '-' {
			t.Errorf("shape of %q", c)
		}
		n, ok := NormalizeRecovery(c)
		if !ok || seen[n] {
			t.Errorf("%q: normalised %q ok %v seen %v", c, n, ok, seen[n])
		}
		seen[n] = true
	}
	if len(codes) != RecoveryCodes {
		t.Errorf("%d codes", len(codes))
	}
	if n, ok := NormalizeRecovery("  K3M9X P2WQA "); !ok || n != "k3m9xp2wqa" {
		t.Errorf("typed form: %q %v", n, ok)
	}
	for _, bad := range []string{"", "k3m9x-p2wq", "k3m9x-p2wqa1", "k3m9o-p2wqa", strings.Repeat("a", 11), "123456"} {
		if _, ok := NormalizeRecovery(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
