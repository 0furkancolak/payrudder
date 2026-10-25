package command

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateNeverCutsACharacter(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{{"abc", 5, "abc"}, {"abcdef", 3, "abc"}, {"\u00e7a\u011fi\u015f", 3, "\u00e7a\u011f"}, {"", 3, ""}} {
		got := truncate(c.in, c.n)
		if got != c.want || !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}
