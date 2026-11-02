// Package webhooksig signs and verifies Payrudder's outbound webhooks. A delivery carries
//
//	Payrudder-Signature: t=1700000000,v1=<hex>[,v1=<hex>]
//
// where each v1 is HMAC-SHA256 over "<t>.<body>" with one of the endpoint's valid secrets (two while a rotated secret is still accepted). A receiver
// recomputes it with its secret, compares in constant time and rejects a timestamp too far from its clock.
//
// That v1 signature covers the time and the body only. It is right for a webhook (the receiver is one endpoint and the body names the event), and wrong for an
// API whose requests are told apart by method, path and query: a GET or DELETE has an empty body, so one captured signature was good for every other empty-bodied
// request to the same listener for the whole tolerance. For those, SignRequest and VerifyRequest use v2:
//
//	Payrudder-Signature: t=1700000000,v2=<hex>[,v2=<hex>]
//
// where each v2 is HMAC-SHA256 over "v2\n<t>\n<METHOD>\n<escaped path>\n<raw query>\n<body>". The four lines before the body cannot contain a newline (the path and
// the query are the escaped forms), so no field slides into the next. The host is not signed: a service name or a proxy in front changes nothing.
package webhooksig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
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
	// ErrV1Refused: the header carries only a v1 signature (time and body) and the receiver requires v2.
	ErrV1Refused = errors.New("webhooksig: a v1 signature is not accepted here")
)

// parse reads t and the signatures of each version from a header.
func parse(header string) (t int64, v1, v2 []string, err error) {
	haveT := false
	for _, p := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			return 0, nil, nil, ErrMalformed
		}
		switch k {
		case "t":
			n, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil {
				return 0, nil, nil, ErrMalformed
			}
			t, haveT = n, true
		case "v1":
			v1 = append(v1, v)
		case "v2":
			v2 = append(v2, v)
		}
	}
	if !haveT {
		return 0, nil, nil, ErrMalformed
	}
	return t, v1, v2, nil
}

func within(t int64, now time.Time, tolerance time.Duration) error {
	if tolerance == 0 {
		tolerance = DefaultTolerance
	}
	if d := now.Sub(time.Unix(t, 0)); d > tolerance || d < -tolerance {
		return ErrTooOld
	}
	return nil
}

func anyEqual(sigs []string, want string) bool {
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return true
		}
	}
	return false
}

// Verify checks a v1 header against body and secret. tolerance 0 means DefaultTolerance. A header that has no v1 signature (a v2 one) is malformed here.
func Verify(header string, body []byte, secret string, now time.Time, tolerance time.Duration) error {
	t, sigs, _, err := parse(header)
	if err != nil || len(sigs) == 0 {
		return ErrMalformed
	}
	if err := within(t, now, tolerance); err != nil {
		return err
	}
	if !anyEqual(sigs, mac(secret, t, body)) {
		return ErrMismatch
	}
	return nil
}

// requestMAC is the v2 signature of a request. u is the request's URL; only its escaped path and raw query are used.
func requestMAC(secret string, t int64, method string, u *url.URL, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte("v2\n" + strconv.FormatInt(t, 10) + "\n" + method + "\n" + u.EscapedPath() + "\n" + u.RawQuery + "\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// SignRequest returns the v2 header value for a request: method, the path and query of u, and body, signed at `at` with each of the secrets (the current one first).
func SignRequest(at time.Time, method string, u *url.URL, body []byte, secrets ...string) string {
	t := at.Unix()
	parts := []string{"t=" + strconv.FormatInt(t, 10)}
	for _, s := range secrets {
		parts = append(parts, "v2="+requestMAC(s, t, method, u, body))
	}
	return strings.Join(parts, ",")
}

// VerifyRequest checks a v2 header against the request it arrived with. A header with a v2 signature is judged by it alone. A header with only a v1 signature is
// ErrV1Refused unless acceptV1, which a receiver sets for the length of a rollout from senders that do not sign v2 yet; then v1 is checked as Verify does (it covers the
// body and the time only). tolerance 0 means DefaultTolerance.
func VerifyRequest(header, method string, u *url.URL, body []byte, secret string, now time.Time, tolerance time.Duration, acceptV1 bool) error {
	t, v1, v2, err := parse(header)
	if err != nil || len(v1)+len(v2) == 0 {
		return ErrMalformed
	}
	if err := within(t, now, tolerance); err != nil {
		return err
	}
	if len(v2) > 0 {
		if !anyEqual(v2, requestMAC(secret, t, method, u, body)) {
			return ErrMismatch
		}
		return nil
	}
	if !acceptV1 {
		return ErrV1Refused
	}
	if !anyEqual(v1, mac(secret, t, body)) {
		return ErrMismatch
	}
	return nil
}
