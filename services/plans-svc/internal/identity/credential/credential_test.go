package credential

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash is not a PHC string with the documented parameters: %q", h)
	}
	if ok, err := Verify("correct horse battery", h); err != nil || !ok {
		t.Fatalf("right password: %v, %v", ok, err)
	}
	if ok, err := Verify("correct horse batterz", h); err != nil || ok {
		t.Fatalf("wrong password: %v, %v", ok, err)
	}
	h2, _ := Hash("correct horse battery")
	if h == h2 {
		t.Error("two hashes of one password share a salt")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, h := range []string{
		"", "plain", "$argon2id$v=19$m=19456,t=2,p=1$AAAA", "$argon2i$v=19$m=19456,t=2,p=1$AAAA$AAAA",
		"$argon2id$v=18$m=19456,t=2,p=1$AAAA$AAAA", "$argon2id$v=19$m=99999999,t=2,p=1$AAAA$AAAA", "$argon2id$v=19$m=1,t=0,p=1$AAAA$AAAA",
	} {
		if _, err := Verify("x", h); err == nil {
			t.Errorf("Verify accepted %q", h)
		}
	}
}

func TestPrefixedTokens(t *testing.T) {
	for _, prefix := range []string{VerifyPrefix, ResetPrefix, InvitePrefix} {
		tok, hash, err := NewPrefixedToken(prefix)
		if err != nil {
			t.Fatal(err)
		}
		if !WellFormed(prefix, tok) || len(hash) != 32 || string(hash) != string(HashToken(tok)) {
			t.Fatalf("%s token malformed: %q", prefix, tok)
		}
		if WellFormed(prefix, tok[:len(tok)-1]) || WellFormed(prefix, "x"+tok) {
			t.Fatalf("%s: a damaged token passed", prefix)
		}
		if other, _, _ := NewPrefixedToken(prefix); other == tok {
			t.Fatal("two tokens are equal")
		}
	}
	tok, _, _ := NewPrefixedToken(ResetPrefix)
	if WellFormed(VerifyPrefix, tok) || WellFormedToken(tok) {
		t.Fatal("token kinds are not interchangeable")
	}
}

func TestTokens(t *testing.T) {
	tok, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if !WellFormedToken(tok) || len(hash) != 32 {
		t.Fatalf("token %q hash %d bytes", tok, len(hash))
	}
	if string(HashToken(tok)) != string(hash) {
		t.Error("HashToken disagrees with NewToken")
	}
	other, _, _ := NewToken()
	if other == tok {
		t.Error("tokens repeat")
	}
	for _, bad := range []string{"", "qs_", "qk_test_abcdefgh_" + strings.Repeat("a", 20), "qs_short", tok + "x"} {
		if WellFormedToken(bad) {
			t.Errorf("%q is not a session token", bad)
		}
	}
}
