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

// A box sealed before keys had ids has 1 as its first byte, the old version byte: a Box built the old way, and a ring that holds key 1, open it as before.
func TestBoxesSealedBeforeKeyIdsStillOpen(t *testing.T) {
	old := []byte{1} // what Seal wrote as its first byte until key ids existed
	plain := boxOf(t, 7)
	sealed, _ := plain.Seal([]byte("secret"), []byte("ctx"))
	if !bytes.HasPrefix(sealed, old) {
		t.Fatalf("a box of the first key starts with %v, want %v", sealed[:1], old)
	}
	ring, err := secretbox.NewRing(2, map[byte][]byte{1: key(7), 2: key(8)})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ring.Open(sealed, []byte("ctx")); err != nil || string(got) != "secret" {
		t.Errorf("a ring holding the old key opens the old box: %q, %v", got, err)
	}
}

// boxOf is a Box of key(b) with the first id.
func boxOf(t *testing.T, b byte) *secretbox.Box {
	t.Helper()
	box, err := secretbox.New(key(b))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

// Two keys are active at once: the new one seals, both open, and a re-seal moves a box from the old to the new without changing what it holds or what it is bound to.
func TestRotationOpensBothKeysAndResealMovesToTheActiveOne(t *testing.T) {
	oldBox := boxOf(t, 1)
	sealedOld, _ := oldBox.Seal([]byte("api key"), []byte("tenant|stripe"))

	ring, err := secretbox.NewRing(2, map[byte][]byte{1: key(1), 2: key(2)})
	if err != nil {
		t.Fatal(err)
	}
	sealedNew, _ := ring.Seal([]byte("api key"), []byte("tenant|stripe"))
	if sealedNew[0] != 2 {
		t.Fatalf("the active key seals with its id: first byte %d, want 2", sealedNew[0])
	}
	for name, box := range map[string][]byte{"sealed by the old key": sealedOld, "sealed by the new key": sealedNew} {
		if got, err := ring.Open(box, []byte("tenant|stripe")); err != nil || string(got) != "api key" {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
	if !ring.Stale(sealedOld) || ring.Stale(sealedNew) || ring.Stale(nil) {
		t.Errorf("Stale: old %t, new %t, empty %t; want true false false", ring.Stale(sealedOld), ring.Stale(sealedNew), ring.Stale(nil))
	}

	moved, err := ring.Reseal(sealedOld, []byte("tenant|stripe"))
	if err != nil {
		t.Fatal(err)
	}
	if ring.Stale(moved) || moved[0] != 2 {
		t.Errorf("a re-sealed box carries the active id: first byte %d", moved[0])
	}
	// With the old key dropped, the re-sealed box still opens and the old one no longer does.
	onlyNew, _ := secretbox.NewRing(2, map[byte][]byte{2: key(2)})
	if got, err := onlyNew.Open(moved, []byte("tenant|stripe")); err != nil || string(got) != "api key" {
		t.Errorf("the re-sealed box without the old key: %q, %v", got, err)
	}
	if _, err := onlyNew.Open(sealedOld, []byte("tenant|stripe")); !errors.Is(err, secretbox.ErrCorrupt) {
		t.Errorf("a box of a key that was dropped: %v, want ErrCorrupt", err)
	}
	// A re-seal keeps the binding: a box moved to another row's context does not open.
	if _, err := ring.Reseal(sealedOld, []byte("other|stripe")); !errors.Is(err, secretbox.ErrCorrupt) {
		t.Errorf("a re-seal under another context: %v, want ErrCorrupt", err)
	}
}

func TestRingRefusesWhatCannotSeal(t *testing.T) {
	for name, tc := range map[string]struct {
		active byte
		keys   map[byte][]byte
	}{
		"the active key is not among them": {3, map[byte][]byte{1: key(1)}},
		"id 0":                             {0, map[byte][]byte{0: key(1)}},
		"a short key":                      {1, map[byte][]byte{1: make([]byte, 16)}},
		"no keys":                          {1, nil},
	} {
		if _, err := secretbox.NewRing(tc.active, tc.keys); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestSettings(t *testing.T) {
	b64 := func(b byte) string { return base64.StdEncoding.EncodeToString(key(b)) }
	for name, tc := range map[string]struct {
		s       secretbox.Settings
		wantBox bool
		wantErr bool
	}{
		"no key: the features are off":       {secretbox.Settings{}, false, false},
		"one key":                            {secretbox.Settings{SecretsKey: b64(1)}, true, false},
		"a rotation":                         {secretbox.Settings{SecretsKey: b64(2), SecretsKeyID: 2, SecretsKeyPrevious: b64(1)}, true, false},
		"a rotation with explicit ids":       {secretbox.Settings{SecretsKey: b64(3), SecretsKeyID: 3, SecretsKeyPrevious: b64(2), SecretsKeyPreviousID: 2}, true, false},
		"both keys have the first id":        {secretbox.Settings{SecretsKey: b64(2), SecretsKeyPrevious: b64(1)}, false, true},
		"a previous key without a key":       {secretbox.Settings{SecretsKeyPrevious: b64(1)}, false, true},
		"a key that is not base64":           {secretbox.Settings{SecretsKey: "!!!"}, false, true},
		"a previous key of the wrong length": {secretbox.Settings{SecretsKey: b64(2), SecretsKeyID: 2, SecretsKeyPrevious: base64.StdEncoding.EncodeToString(make([]byte, 8))}, false, true},
		"an id above 255":                    {secretbox.Settings{SecretsKey: b64(1), SecretsKeyID: 256}, false, true},
		"a negative previous id":             {secretbox.Settings{SecretsKey: b64(2), SecretsKeyID: 2, SecretsKeyPrevious: b64(1), SecretsKeyPreviousID: -1}, false, true},
	} {
		box, err := tc.s.SecretBox()
		if (err != nil) != tc.wantErr || (box != nil) != tc.wantBox {
			t.Errorf("%s: box %v, err %v", name, box != nil, err)
		}
	}
	// What a rotation configures: the old deployment's boxes (key 1) open, new ones carry id 2.
	rot, _ := secretbox.Settings{SecretsKey: b64(2), SecretsKeyID: 2, SecretsKeyPrevious: b64(1)}.SecretBox()
	oldSealed, _ := boxOf(t, 1).Seal([]byte("x"), nil)
	if got, err := rot.Open(oldSealed, nil); err != nil || string(got) != "x" {
		t.Errorf("the settings of a rotation open a box of the old key: %q, %v", got, err)
	}
	if fresh, _ := rot.Seal([]byte("x"), nil); fresh[0] != 2 {
		t.Errorf("the settings of a rotation seal with id 2, got %d", fresh[0])
	}
}
