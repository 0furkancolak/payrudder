package main

import (
	"testing"

	"github.com/0furkancolak/quotify/pkg/config"
)

// The embedded defaults name only settings this service has, hold no secret and give no default to a required setting.
func TestConfigDefaults(t *testing.T) {
	if err := config.CheckDefaults[Config](defaults); err != nil {
		t.Fatal(err)
	}
}

// Startup refuses the mock as the live provider unless the stack says it is not production; a name that is not a provider is refused whatever else is set.
func TestLiveProviderStartupRule(t *testing.T) {
	for _, tc := range []struct {
		provider        string
		allowMock, demo bool
		ok              bool
	}{
		{"", false, false, true},
		{"mockpsp", false, false, false},
		{"mockpsp", true, false, true},
		{"mockpsp", false, true, true},
		{"mock", true, true, false},
		{"MockPSP", true, true, false},
		{"iyzico", true, true, false},
	} {
		if err := checkLiveProvider(tc.provider, tc.allowMock, tc.demo); (err == nil) != tc.ok {
			t.Errorf("PAYMENTS_LIVE_PROVIDER=%q allow=%v demo=%v: %v, want ok=%v", tc.provider, tc.allowMock, tc.demo, err, tc.ok)
		}
	}
}
