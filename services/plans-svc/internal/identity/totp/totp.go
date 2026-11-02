// Package totp is the second factor of a sign-in (plan 22, G9): time-based one-time passwords as RFC 6238 defines them (HMAC-SHA-1, six digits, 30-second steps), the
// shared secret as base32 for an authenticator app, and the single-use recovery codes. It has no I/O and never logs.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 and every authenticator app: the algorithm is HMAC-SHA-1; the secret is 160 random bits, which SHA-1's weakness does not touch
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RFC 6238 parameters as the authenticator apps read them from the otpauth address.
const (
	Digits      = 6
	Period      = 30 // seconds
	SecretBytes = 20 // 160 bits, the length of the SHA-1 output (RFC 4226 section 4)
	// Window is how many steps either side of the clock's a code is accepted for: a phone's clock is rarely exact.
	Window = 1
)

// NewSecret returns a fresh random secret.
func NewSecret() ([]byte, error) {
	b := make([]byte, SecretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("totp: secret: %w", err)
	}
	return b, nil
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// Encode is the secret as an authenticator app takes it typed in: base32, upper case, no padding.
func Encode(secret []byte) string { return b32.EncodeToString(secret) }

// Step is the 30-second step that contains t.
func Step(t time.Time) int64 { return t.Unix() / Period }

// code is HOTP (RFC 4226) of the counter with the given number of digits.
func code(secret []byte, counter int64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter)) //nolint:gosec // a step is a time since 1970, never negative
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// Code is the code of the step.
func Code(secret []byte, step int64) string { return code(secret, step, Digits) }

// Match finds the step a code is valid for: one within Window of now, later than after (the last step a code was accepted for, so a code works once). The compare
// takes the same time whatever the code is.
func Match(secret []byte, given string, now time.Time, after int64) (step int64, ok bool) {
	if len(given) != Digits {
		return 0, false
	}
	cur := Step(now)
	for s := cur - Window; s <= cur+Window; s++ {
		if s > after && subtle.ConstantTimeCompare([]byte(Code(secret, s)), []byte(given)) == 1 {
			step, ok = s, true // no early return: the loop's time must not tell which step matched
		}
	}
	return step, ok
}

// URI is the otpauth address an authenticator app reads from a QR code: Key URI Format, with the issuer both in the label and as a parameter.
func URI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{"secret": {Encode(secret)}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {fmt.Sprint(Digits)}, "period": {fmt.Sprint(Period)}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Recovery codes: ten characters from an alphabet without the look-alikes (no 0, o, 1, i, l), written as two groups of five.
const (
	RecoveryCodes   = 10
	recoveryAlpha   = "abcdefghjkmnpqrstuvwxyz23456789"
	recoveryLen     = 10
	recoveryGroupBy = 5
)

// NewRecoveryCodes returns RecoveryCodes fresh codes such as "k3m9x-p2wqa".
func NewRecoveryCodes() ([]string, error) {
	// A byte is used only below the largest multiple of the alphabet's length, so every character is equally likely.
	limit := 256 / len(recoveryAlpha) * len(recoveryAlpha)
	out := make([]string, RecoveryCodes)
	for i := range out {
		var sb strings.Builder
		for sb.Len() < recoveryLen+1 {
			if sb.Len() == recoveryGroupBy {
				sb.WriteByte('-')
			}
			var b [1]byte
			if _, err := rand.Read(b[:]); err != nil {
				return nil, fmt.Errorf("totp: recovery code: %w", err)
			}
			if int(b[0]) < limit {
				sb.WriteByte(recoveryAlpha[int(b[0])%len(recoveryAlpha)])
			}
		}
		out[i] = sb.String()
	}
	return out, nil
}

// NormalizeRecovery turns what a person typed into the stored form (lower case, no spaces or dashes) and reports whether it can be a recovery code at all.
func NormalizeRecovery(s string) (string, bool) {
	s = strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(s)))
	if len(s) != recoveryLen {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(recoveryAlpha, rune(s[i])) {
			return "", false
		}
	}
	return s, true
}
