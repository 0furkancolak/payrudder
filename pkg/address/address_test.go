package address

import (
	"slices"
	"testing"
)

func TestLines(t *testing.T) {
	for _, c := range []struct {
		name string
		got  []string
		want []string
	}{
		{"full", Lines("Atatürk Cd. 1", "Kat 2", "İstanbul", "Marmara", "34000", "TR"), []string{"Atatürk Cd. 1", "Kat 2", "34000 İstanbul", "Marmara", "TR"}},
		{"city only", Lines("", "", "Berlin", "", "", "DE"), []string{"Berlin", "DE"}},
		{"postal code only", Lines("", "", "", "", "10115", ""), []string{"10115"}},
		{"nothing", Lines("", "", "", "", "", ""), nil},
		{"blanks", Lines(" ", "", "  ", "", "", ""), nil},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
}
