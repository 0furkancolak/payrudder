package query

import (
	"strings"
	"testing"
)

func TestCurrencyExponentFallsBackToTwo(t *testing.T) {
	for code, want := range map[string]int32{"USD": 2, "JPY": 0, "KWD": 3, "NOPE": 2, "": 2} {
		if got := CurrencyExponent(code); got != want {
			t.Errorf("CurrencyExponent(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestValidTokenIsTwentyToOneHundredCharacters(t *testing.T) {
	for n, want := range map[int]bool{0: false, 19: false, 20: true, 43: true, 100: true, 101: false} {
		if got := validToken(strings.Repeat("a", n)); got != want {
			t.Errorf("validToken(%d characters) = %v, want %v", n, got, want)
		}
	}
}
