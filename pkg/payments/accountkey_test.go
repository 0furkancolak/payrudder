package payments

import "testing"

func TestAccountKey(t *testing.T) {
	for _, c := range []struct {
		key, base, label string
		ok               bool
	}{
		{"stripe", "stripe", "", true},
		{"stripe~eu", "stripe", "eu", true},
		{"stripe~", "stripe", "", false},
		{"stripe~EU", "stripe", "EU", false},
		{"stripe~a~b", "stripe", "a~b", false},
		{"~eu", "", "eu", false},
		{"stripe~eu west", "stripe", "eu west", false},
	} {
		if BaseProvider(c.key) != c.base || AccountLabel(c.key) != c.label || ValidAccountKey(c.key) != c.ok {
			t.Errorf("%q: base %q label %q valid %v", c.key, BaseProvider(c.key), AccountLabel(c.key), ValidAccountKey(c.key))
		}
	}
	if AccountKey("paddle", "") != "paddle" || AccountKey("paddle", "uk") != "paddle~uk" {
		t.Error("AccountKey")
	}
}
