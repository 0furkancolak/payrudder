// Package webhooksig signs and verifies Payrudder's outbound webhooks. A delivery carries
//
//	Payrudder-Signature: t=1700000000,v1=<hex>[,v1=<hex>]
//
// where each v1 is HMAC-SHA256 over "<t>.<body>" with one of the endpoint's valid secrets (two while a rotated secret is still accepted). A receiver
// recomputes it with its secret, compares in constant time and rejects a timestamp too far from its clock.
package webhooksig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/brand"
)

// Header is the name of the signature header: "Payrudder-Signature", or the renamed product's.
func Header() string { return brand.Header("Signature") }

// DefaultTolerance is how far a signed timestamp may be from the receiver's clock.
const DefaultTolerance = 5 * time.Minute

func mac(secret string, t int64, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(strconv.FormatInt(t, 10)))
	h.Write([]byte{'.'})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Sign returns the header value for body, signed at `at` with each of the secrets (the current one first).
func Sign(at time.Time, body []byte, secrets ...string) string {
	t := at.Unix()
	parts := []string{"t=" + strconv.FormatInt(t, 10)}
	for _, s := range secrets {
		parts = append(parts, "v1="+mac(s, t, body))
	}
	return strings.Join(parts, ",")
}

// Verification failures.
var (
	ErrMalformed = errors.New("webhooksig: malformed signature header")
	ErrMismatch  = errors.New("webhooksig: no signature matches")
	ErrTooOld    = errors.New("webhooksig: timestamp outside the tolerance")
)

// Verify checks header against body and secret. tolerance 0 means DefaultTolerance.
func Verify(header string, body []byte, secret string, now time.Time, tolerance time.Duration) error {
	if tolerance == 0 {
		tolerance = DefaultTolerance
	}
	var t int64
	var sigs []string
	haveT := false
	for _, p := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			return ErrMalformed
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return ErrMalformed
			}
			t, haveT = n, true
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if !haveT || len(sigs) == 0 {
		return ErrMalformed
	}
	if d := now.Sub(time.Unix(t, 0)); d > tolerance || d < -tolerance {
		return ErrTooOld
	}
	want := mac(secret, t, body)
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return nil
		}
	}
	return ErrMismatch
}
