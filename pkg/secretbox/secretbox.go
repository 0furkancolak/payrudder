// Package secretbox seals small secrets (a payment provider's API keys) with AES-256-GCM, so they sit in the database
// encrypted. The key comes from the environment, never from the database next to the data.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const version byte = 1

// ErrCorrupt: the box is truncated, from another version, or was sealed with another key.
var ErrCorrupt = errors.New("secretbox: cannot open (wrong key or damaged data)")

// Box seals and opens with one key.
type Box struct{ aead cipher.AEAD }

// ParseKey reads a base64 (standard or URL, padded or not) 32-byte key.
func ParseKey(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) != 32 {
				return nil, fmt.Errorf("secretbox: key must be 32 bytes, got %d", len(b))
			}
			return b, nil
		}
	}
	return nil, errors.New("secretbox: key is not base64")
}

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secretbox: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext. aad binds the box to its context (for example the tenant and provider), so a box copied to another
// row does not open there.
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{version}, nonce...)
	return b.aead.Seal(out, nonce, plaintext, aad), nil
}

// Open decrypts a box made by Seal with the same aad.
func (b *Box) Open(box, aad []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(box) < 1+n+b.aead.Overhead() || box[0] != version {
		return nil, ErrCorrupt
	}
	pt, err := b.aead.Open(nil, box[1:1+n], box[1+n:], aad)
	if err != nil {
		return nil, ErrCorrupt
	}
	return pt, nil
}
