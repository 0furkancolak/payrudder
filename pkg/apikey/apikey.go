// Package apikey defines the API key format shared by plans-svc (issuing) and the gateway (verifying):
//
//	qk_{live|test}_{prefix}_{secret}
//
// prefix is 8 base62 characters (indexed, not secret). secret is 32 random bytes in base62 (~190 bits).
// Only sha256(secret) is stored. A slow hash isn't needed for high-entropy random secrets (docs/06-multi-tenancy.md#api-keys).
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math/big"
	"strings"
)

// Env is the key environment: test keys resolve to the sandbox tenant, live keys to the live tenant.
type Env string

const (
	EnvTest Env = "test"
	EnvLive Env = "live"
)

// PrefixLen is the length of the lookup prefix.
const PrefixLen = 8

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// CacheKey is the Redis key under which the gateway caches the resolution of the key with this prefix. plans-svc deletes it when a key is revoked or
// changed, so both services build the key here and a change cannot leave a revoked key in the cache.
func CacheKey(prefix string) string { return "gw:apikey:" + prefix }

// ErrMalformed is returned by Parse for anything that isn't a well-formed key.
var ErrMalformed = errors.New("apikey: malformed key")

// Key is a parsed key.
type Key struct {
	Env    Env
	Prefix string
	Secret string
}

// String renders the plaintext key.
func (k Key) String() string {
	return "qk_" + string(k.Env) + "_" + k.Prefix + "_" + k.Secret
}

// Hash returns sha256(secret), the stored form.
func (k Key) Hash() []byte {
	h := sha256.Sum256([]byte(k.Secret))
	return h[:]
}

// Matches compares the key's secret with a stored hash in constant time.
func (k Key) Matches(storedHash []byte) bool {
	return subtle.ConstantTimeCompare(k.Hash(), storedHash) == 1
}

// Generate creates a new random key.
func Generate(env Env) (Key, error) {
	if env != EnvTest && env != EnvLive {
		return Key{}, errors.New("apikey: unknown env")
	}
	prefix, err := randomBase62(PrefixLen)
	if err != nil {
		return Key{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Key{}, err
	}
	return Key{Env: env, Prefix: prefix, Secret: encodeBase62(raw)}, nil
}

// Parse splits a plaintext key. It checks shape only; authenticity needs the stored hash.
func Parse(s string) (Key, error) {
	rest, ok := strings.CutPrefix(s, "qk_")
	if !ok {
		return Key{}, ErrMalformed
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 3 {
		return Key{}, ErrMalformed
	}
	k := Key{Env: Env(parts[0]), Prefix: parts[1], Secret: parts[2]}
	if (k.Env != EnvTest && k.Env != EnvLive) || len(k.Prefix) != PrefixLen || !isBase62(k.Prefix) ||
		len(k.Secret) < 40 || len(k.Secret) > 44 || !isBase62(k.Secret) {
		return Key{}, ErrMalformed
	}
	return k, nil
}

func isBase62(s string) bool {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(alphabet, s[i]) < 0 {
			return false
		}
	}
	return true
}

func randomBase62(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[v.Int64()]
	}
	return string(b), nil
}

func encodeBase62(raw []byte) string {
	n := new(big.Int).SetBytes(raw)
	base := big.NewInt(int64(len(alphabet)))
	mod := new(big.Int)
	var out []byte
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, alphabet[mod.Int64()])
	}
	// Left-pad to a fixed width so every key has the same length (43 chars for 32 bytes).
	for len(out) < 43 {
		out = append(out, alphabet[0])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
