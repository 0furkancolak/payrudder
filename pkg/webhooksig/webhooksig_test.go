package webhooksig

import (
	"errors"
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
