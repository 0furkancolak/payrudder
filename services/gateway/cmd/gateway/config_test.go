package main

import (
	"testing"

	"github.com/0furkancolak/payrudder/pkg/config"
)

func TestParseProxies(t *testing.T) {
	got, err := parseProxies([]string{"10.1.2.3/8", " 192.0.2.7 ", "", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.0.2.7/32", "2001:db8::/32"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("%d: %s, want %s", i, got[i], want[i])
		}
	}
	if _, err := parseProxies([]string{"nope"}); err == nil {
		t.Error("a malformed entry must be rejected")
	}
}

// The embedded defaults name only settings this service has, hold no secret and give no default to a required setting.
func TestConfigDefaults(t *testing.T) {
	if err := config.CheckDefaults[Config](defaults); err != nil {
		t.Fatal(err)
	}
}
