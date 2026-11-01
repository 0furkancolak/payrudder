// Package secretbox seals small secrets (a payment provider's API keys) with AES-256-GCM, so they sit in the database
// encrypted. The key comes from the environment, never from the database next to the data.
//
// The first byte of a box is the id of the key that sealed it (1 to 255). It was the version of the format, always 1, before keys had ids, so every box sealed so far
// is a box of key 1 and opens as before. A Box seals with one key and opens with every key it holds, which is how a key is replaced without a stop: add the new key under
// a new id, make it the one that seals, re-seal what is stored (Stale and Reseal pick and redo the boxes of the old key), and drop the old key when nothing carries its id.
// docs/runbooks/secrets-key-rotation.md has the steps and the places that hold boxes.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// FirstKeyID is the id of the key that sealed every box made before keys had ids, and the id New gives its key.
const FirstKeyID byte = 1

// ErrCorrupt: the box is truncated, from a key this Box does not hold, or was sealed with another key.
var ErrCorrupt = errors.New("secretbox: cannot open (wrong key or damaged data)")

// Box seals with one key and opens with every key it holds.
type Box struct {
	active byte
	aeads  map[byte]cipher.AEAD
}

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

// New returns a Box for a 32-byte key, which has the id FirstKeyID.
func New(key []byte) (*Box, error) { return NewRing(FirstKeyID, map[byte][]byte{FirstKeyID: key}) }

// NewRing returns a Box that seals with the key whose id is active and opens with all of keys (32 bytes each, ids 1 to 255). active must be one of them.
func NewRing(active byte, keys map[byte][]byte) (*Box, error) {
	if _, ok := keys[active]; !ok {
		return nil, fmt.Errorf("secretbox: the key that seals (id %d) is not among the keys", active)
	}
	b := &Box{active: active, aeads: make(map[byte]cipher.AEAD, len(keys))}
	for id, key := range keys {
		if id == 0 {
			return nil, errors.New("secretbox: key ids are 1 to 255")
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("secretbox: key %d must be 32 bytes, got %d", id, len(key))
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		if b.aeads[id], err = cipher.NewGCM(block); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// Seal encrypts plaintext with the active key. aad binds the box to its context (for example the tenant and provider), so a box copied to another
// row does not open there.
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	aead := b.aeads[b.active]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{b.active}, nonce...)
	return aead.Seal(out, nonce, plaintext, aad), nil
}

// Open decrypts a box made by Seal with the same aad, whichever of the Box's keys sealed it.
func (b *Box) Open(box, aad []byte) ([]byte, error) {
	if len(box) == 0 {
		return nil, ErrCorrupt
	}
	aead, ok := b.aeads[box[0]]
	if !ok {
		return nil, ErrCorrupt
	}
	n := aead.NonceSize()
	if len(box) < 1+n+aead.Overhead() {
		return nil, ErrCorrupt
	}
	pt, err := aead.Open(nil, box[1:1+n], box[1+n:], aad)
	if err != nil {
		return nil, ErrCorrupt
	}
	return pt, nil
}

// Stale reports whether the box was sealed with a key other than the active one: the boxes a re-seal still has to redo.
func (b *Box) Stale(box []byte) bool { return len(box) > 0 && box[0] != b.active }

// Reseal opens a box with the key that sealed it and seals its plaintext again with the active key, for the same aad. A box of the active key is sealed anew too (with a
// new nonce); a re-seal job asks Stale first.
func (b *Box) Reseal(box, aad []byte) ([]byte, error) {
	pt, err := b.Open(box, aad)
	if err != nil {
		return nil, err
	}
	return b.Seal(pt, aad)
}
