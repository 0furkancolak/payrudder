// Package credential holds the secrets side of sign-in: password hashing (argon2id), session tokens and their stored hash.
// It has no I/O and never logs: a password or a token must not appear in any message this package builds.
package credential

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: the OWASP minimum for argon2id (19 MiB, two passes, one lane). A hash takes about 20-40 ms and 19 MiB while it runs, which is
// what a small gateway-sized pod can afford a few at a time (the command limits how many run at once). The parameters travel inside the hash, so they
// can be raised later: old hashes still verify with the numbers they were made with.
const (
	memoryKiB  = 19 * 1024
	iterations = 2
	lanes      = 1
	saltLen    = 16
	keyLen     = 32
	// maxMemoryKiB caps the memory a stored hash may ask for when verifying, so a corrupted row cannot make a verify allocate gigabytes.
	maxMemoryKiB = 256 * 1024
)

// TokenPrefix starts every session token, so a leaked one is recognisable (and never confused with a qk_ API key).
const TokenPrefix = "qs_"

// MinPasswordLen and MaxPasswordLen bound a password. The cap keeps a request from making the hash read megabytes.
const (
	MinPasswordLen = 10
	MaxPasswordLen = 256
)

// Hash returns the PHC string of the argon2id hash of password with a fresh random salt.
func Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("credential: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, iterations, memoryKiB, lanes, keyLen)
	return encode(salt, key, memoryKiB, iterations, lanes), nil
}

func encode(salt, key []byte, m, t uint32, p uint8) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, m, t, p, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// ErrMalformedHash means a stored hash is not a PHC argon2id string this package understands.
var ErrMalformedHash = errors.New("credential: malformed password hash")

// Verify reports whether password matches the PHC string hash, comparing in constant time. A hash that cannot be parsed is an error, not a mismatch.
func Verify(password, hash string) (bool, error) {
	parts := strings.Split(hash, "$")
	// ["", "argon2id", "v=19", "m=…,t=…,p=…", salt, key]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrMalformedHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || m > maxMemoryKiB || t == 0 || t > 10 || p == 0 {
		return false, ErrMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, ErrMalformedHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrMalformedHash
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want))) //nolint:gosec // decoded from a stored hash, a few dozen bytes
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified against when the e-mail is unknown, so that an unknown user costs the same time as a wrong password.
var dummyHash = sync.OnceValue(func() string {
	h, err := Hash("not-a-real-password-for-timing-only")
	if err != nil {
		panic(err)
	}
	return h
})

// BurnTime does the work of a verify without a user behind it.
func BurnTime(password string) { _, _ = Verify(password, dummyHash()) }

// Prefixes of the single-use tokens mailed to people. Like qs_ they only make a leaked one recognisable; the secret is the 256 random bits after it.
const (
	VerifyPrefix = "qv_"
	ResetPrefix  = "qr_"
	InvitePrefix = "qi_"
)

// NewPrefixedToken returns a fresh token that starts with prefix (256 random bits, URL-safe) and the hash to store.
func NewPrefixedToken(prefix string) (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("credential: token: %w", err)
	}
	token = prefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// WellFormed reports whether s looks like a token with this prefix, before the database is asked about it.
func WellFormed(prefix, s string) bool {
	return strings.HasPrefix(s, prefix) && len(s) == len(prefix)+43
}

// NewToken returns a fresh session token (qs_ and 256 random bits, URL-safe) and the hash to store. The token is shown once.
func NewToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("credential: token: %w", err)
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken is what the database stores for a token: its SHA-256. The token carries 256 bits, so a plain hash needs no salt or stretching.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// WellFormedToken reports whether s looks like a session token, before the database is asked about it.
func WellFormedToken(s string) bool {
	return strings.HasPrefix(s, TokenPrefix) && len(s) == len(TokenPrefix)+43
}
