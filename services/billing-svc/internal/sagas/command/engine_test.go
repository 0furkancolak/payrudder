package command

import (
	"testing"
	"time"
)

// The grace period of a "not found" answer must end before the provider forgets the key, or no answer could ever be trusted to charge again.
func TestNewEngineChecksTheNotFoundWindow(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg Config
		ok  bool
	}{
		"the defaults":                   {Config{}, true},
		"a longer horizon":               {Config{NotFoundGrace: time.Hour, KeyHorizon: 48 * time.Hour}, true},
		"grace as long as the horizon":   {Config{NotFoundGrace: time.Hour, KeyHorizon: time.Hour}, false},
		"grace longer than the horizon":  {Config{NotFoundGrace: 48 * time.Hour}, false},
		"a sandbox grace past the limit": {Config{NotFoundGraceSandbox: 48 * time.Hour}, false},
	} {
		if _, err := NewEngine(tc.cfg); (err == nil) != tc.ok {
			t.Errorf("%s: NewEngine error = %v, want ok=%v", name, err, tc.ok)
		}
	}
}
