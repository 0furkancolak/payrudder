package secretbox_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/secretbox"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSealOpen(t *testing.T) {
	box, _ := secretbox.New(key(1))
	sealed, err := box.Seal([]byte(`{"api_key":"k"}`), []byte("tenant|iyzico"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("api_key")) {
		t.Fatal("the plaintext is visible in the box")
	}
	got, err := box.Open(sealed, []byte("tenant|iyzico"))
	if err != nil || string(got) != `{"api_key":"k"}` {
		t.Fatalf("Open = %q, %v", got, err)
	}
	again, _ := box.Seal([]byte(`{"api_key":"k"}`), []byte("tenant|iyzico"))
	if bytes.Equal(sealed, again) {
		t.Error("two seals of the same plaintext are identical: the nonce is not random")
	}
}

func TestOpenRefuses(t *testing.T) {
	a, _ := secretbox.New(key(1))
	b, _ := secretbox.New(key(2))
	sealed, _ := a.Seal([]byte("secret"), []byte("ctx"))
	for name, f := range map[string]func() ([]byte, error){
		"another key":     func() ([]byte, error) { return b.Open(sealed, []byte("ctx")) },
		"another context": func() ([]byte, error) { return a.Open(sealed, []byte("other")) },
		"truncated":       func() ([]byte, error) { return a.Open(sealed[:10], []byte("ctx")) },
		"empty":           func() ([]byte, error) { return a.Open(nil, nil) },
		"flipped byte": func() ([]byte, error) {
			c := append([]byte(nil), sealed...)
			c[len(c)-1] ^= 1
			return a.Open(c, []byte("ctx"))
		},
		"another version": func() ([]byte, error) {
			c := append([]byte(nil), sealed...)
			c[0] = 9
			return a.Open(c, []byte("ctx"))
		},
	} {
		if _, err := f(); !errors.Is(err, secretbox.ErrCorrupt) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestKeys(t *testing.T) {
	if _, err := secretbox.New(make([]byte, 16)); err == nil {
		t.Error("a 16-byte key must be refused")
	}
	k, err := secretbox.ParseKey(base64.StdEncoding.EncodeToString(key(7)))
	if err != nil || !bytes.Equal(k, key(7)) {
		t.Errorf("ParseKey = %v, %v", k, err)
	}
	if _, err := secretbox.ParseKey(base64.StdEncoding.EncodeToString(make([]byte, 8))); err == nil {
		t.Error("a short key must be refused")
	}
	if _, err := secretbox.ParseKey("!!!"); err == nil {
		t.Error("garbage must be refused")
	}
}
