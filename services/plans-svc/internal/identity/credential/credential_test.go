package credential

import (
	"encoding/hex"
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

// Version 1 of the recovery hash is what every recovery code made before the security review is stored with: this value was computed by the code as it was then, so a
// change of HashRecoveryV1 would lock those people out.
func TestRecoveryHashV1IsWhatWasStoredBefore(t *testing.T) {
	got := hex.EncodeToString(HashRecoveryV1([]byte("test-pepper-not-a-secret"), "3f2504e0-4f89-11d3-9a0c-0305e82c3301", "ABCDEFGHJK"))
	if want := "5f81da89cd483e196950fa7cc8151254ed288d73269ffa3ec484eeed3bbf947a"; got != want {
		t.Fatalf("HashRecoveryV1 = %s, want %s", got, want)
	}
}

// Version 2 keys the hash with a key derived for recovery codes alone: it is not the old hash, it depends on the pepper, the user and the code, and the pepper
// itself (which also keys the six-digit sign-in codes) is not the key.
func TestRecoveryHashV2HasItsOwnKey(t *testing.T) {
	pepper, user, code := []byte("test-pepper-not-a-secret"), "3f2504e0-4f89-11d3-9a0c-0305e82c3301", "ABCDEFGHJK"
	v2 := HashRecovery(pepper, user, code)
	if len(v2) != 32 || !EqualHash(v2, HashRecovery(pepper, user, code)) {
		t.Fatalf("version 2 is not a stable 32-byte hash: %x", v2)
	}
	if EqualHash(v2, HashRecoveryV1(pepper, user, code)) {
		t.Error("version 2 equals version 1")
	}
	for name, other := range map[string][]byte{
		"another pepper": HashRecovery([]byte("another-pepper"), user, code),
		"another user":   HashRecovery(pepper, "00000000-0000-0000-0000-000000000000", code),
		"another code":   HashRecovery(pepper, user, "KJHGFEDCBA"),
		"no pepper":      HashRecovery(nil, user, code),
	} {
		if EqualHash(v2, other) {
			t.Errorf("%s gives the same hash", name)
		}
	}
	// What a leaked pepper plus a leaked row would let somebody do with the key of the other use: the sign-in code hash is not a recovery hash, nor the other way round.
	if EqualHash(v2, HashCode(pepper, []byte(user), "", code)) {
		t.Error("a recovery hash equals a sign-in code hash")
	}
	if RecoveryHashVersion != 2 {
		t.Errorf("new recovery codes are stored with version %d", RecoveryHashVersion)
	}
}
