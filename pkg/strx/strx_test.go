package strx_test

import (
	"testing"

	"github.com/0furkancolak/payrudder/pkg/strx"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"", 0, ""},
		{"çç", 3, "ç"},  // the second ç would be cut in half
		{"çç", 4, "çç"}, // fits exactly
		{"日本語", 4, "日"},
	}
	for _, c := range cases {
		if got := strx.Truncate(c.in, c.n); got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestDeref(t *testing.T) {
	s := "x"
	if strx.Deref(&s) != "x" || strx.Deref(nil) != "" {
		t.Fatal("Deref")
	}
}
