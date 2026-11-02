package mailx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Svix is the signature scheme Resend signs its webhooks with. Read 2026-11-02 at https://docs.svix.com/receiving/verifying-payloads/how-manual: the headers svix-id,
// svix-timestamp (seconds) and svix-signature (space-separated "v1,<base64>" entries); the signed content is "<id>.<timestamp>.<raw body>"; the key is the base64 of
// the secret after its "whsec_" prefix; the signature is the base64 of the HMAC-SHA256; any one entry matching is enough. UNVERIFIED: that Nafru signs its webhooks
// this way (Nafru is described as Resend-compatible; its documentation was not read).

// SvixTolerance is how far a timestamp may be from the clock: five minutes, the usual window of a replay check (the Svix page names no value).
const SvixTolerance = 5 * time.Minute

var errSvixSignature = errors.New("the webhook signature is missing or does not verify")

// svixKey decodes a "whsec_..." secret.
func svixKey(secret string) ([]byte, error) {
	raw, ok := strings.CutPrefix(secret, "whsec_")
	if !ok {
		return nil, errors.New("a webhook secret starts with whsec_")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) < 16 {
		return nil, errors.New("a webhook secret is whsec_ and the base64 of at least 16 bytes")
	}
	return key, nil
}

// ValidSvixSecret reports whether secret can sign and verify (the form of a Svix signing secret).
func ValidSvixSecret(secret string) error {
	_, err := svixKey(secret)
	return err
}

// SvixSign returns the svix-signature header value for a message (used by tests and the fake provider).
func SvixSign(secret, id string, at time.Time, body []byte) (string, error) {
	key, err := svixKey(secret)
	if err != nil {
		return "", err
	}
	return "v1," + svixMAC(key, id, strconv.FormatInt(at.Unix(), 10), body), nil
}

func svixMAC(key []byte, id, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + timestamp + "."))
	m.Write(body)
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// VerifySvix checks the three headers of a request against the raw body and the secret, and the timestamp against now within tolerance (either side: a clock that
// runs ahead is as suspect as an old message).
func VerifySvix(h http.Header, body []byte, secret string, now time.Time, tolerance time.Duration) error {
	key, err := svixKey(secret)
	if err != nil {
		return err
	}
	id, ts, sigs := h.Get("svix-id"), h.Get("svix-timestamp"), h.Get("svix-signature")
	secs, perr := strconv.ParseInt(ts, 10, 64)
	if id == "" || sigs == "" || perr != nil {
		return errSvixSignature
	}
	if d := now.Sub(time.Unix(secs, 0)); d > tolerance || d < -tolerance {
		return errors.New("the webhook timestamp is outside the tolerance")
	}
	want := svixMAC(key, id, ts, body)
	for _, entry := range strings.Fields(sigs) {
		version, sig, ok := strings.Cut(entry, ",")
		if ok && version == "v1" && hmac.Equal([]byte(sig), []byte(want)) {
			return nil
		}
	}
	return errSvixSignature
}
