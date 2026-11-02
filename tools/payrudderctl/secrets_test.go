package main

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/secretbox"
)

// The variables of a rotation in the middle (key 2 seals, key 1 only opens) make the Box that moves a key-1 box to key 2.
func TestSecretSettingsHoldTwoKeys(t *testing.T) {
	k1, k2 := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	env := map[string]string{
		"PAYRUDDER_SECRETS_KEY": base64.StdEncoding.EncodeToString(k2), "PAYRUDDER_SECRETS_KEY_ID": "2",
		"PAYRUDDER_SECRETS_KEY_PREVIOUS": base64.StdEncoding.EncodeToString(k1), // its id is empty: 1
	}
	s, err := secretSettings(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	box, err := s.SecretBox()
	if err != nil || box.ActiveID() != 2 {
		t.Fatalf("box = %v, %v", box, err)
	}
	old, _ := secretbox.New(k1)
	sealed, _ := old.Seal([]byte("secret"), []byte("aad"))
	if !box.Stale(sealed) {
		t.Fatal("a box of key 1 is not stale for a Box that seals with key 2")
	}
	if again, err := box.Reseal(sealed, []byte("aad")); err != nil || again[0] != 2 {
		t.Fatalf("reseal = %v, %v", again, err)
	}
	env["PAYRUDDER_SECRETS_KEY_ID"] = "two"
	if _, err := secretSettings(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "PAYRUDDER_SECRETS_KEY_ID") {
		t.Errorf("an id that is not a number: err = %v", err)
	}
}

// It refuses before it opens a connection: a service it does not know, no key, no database.
func TestSecretsResealRefusesWhatItCannotRun(t *testing.T) {
	t.Setenv("PAYRUDDER_SECRETS_KEY", "")
	t.Setenv("PAYRUDDER_PG_SYSTEM_DSN", "")
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no subcommand":   {[]string{}, "usage"},
		"another command": {[]string{"rotate"}, "usage"},
		"no service":      {[]string{"reseal"}, "--service"},
		"another service": {[]string{"reseal", "--service", "billing"}, "--service"},
		"no key":          {[]string{"reseal", "--service", "plans"}, "PAYRUDDER_SECRETS_KEY"},
	} {
		if err := secretsCmd(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	t.Setenv("PAYRUDDER_SECRETS_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if err := secretsCmd([]string{"reseal", "--service", "plans"}); err == nil || !strings.Contains(err.Error(), "PAYRUDDER_PG_SYSTEM_DSN") {
		t.Errorf("no database: err = %v", err)
	}
}
