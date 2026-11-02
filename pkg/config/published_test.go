package config_test

import (
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/config"
)

// The defaults of the compose stack and the Helm chart, and the placeholders of values-secrets.example.yaml, are public: outside a development environment none of
// them may be a secret. A value of one's own, and an empty one (a feature that is off), pass.
func TestCheckSecretRefusesPublishedValuesOutsideDevelopment(t *testing.T) {
	published := []string{
		"local-operator-token-change-me-0123456789",
		"local-mail-events-secret-change-me-0123456789",
		"local-internal-token-change-me-0123456789",
		"bG9jYWwtZGV2LWtleS1jaGFuZ2UtbWUtMDEyMzQ1Njc=",
		"CHANGE-ME-at-least-32-characters-long-xxxxxxxxxxxx",
		"CHANGE-ME-16-characters-or-more-xxxxxxxxx",
		"CHANGE-ME-32-bytes-base64",
		"CHANGE-ME-32-characters-or-more-xxxxxxxxx",
	}
	own := "k3Jx9mQ2vLw8Rt5Yh7Ud1Zn4Bc6Pf0Sa+GeIoVxTqM="
	for _, e := range []config.Environment{config.EnvLocal, config.EnvCI, config.EnvKind} {
		b := config.Base{Env: e}
		for _, v := range append(published, "", own) {
			if err := b.CheckSecret("X", v); err != nil {
				t.Errorf("%s with %q: %v", e, v, err)
			}
		}
	}
	for _, e := range []config.Environment{config.EnvRailway, config.EnvProduction} {
		b := config.Base{Env: e}
		for _, v := range published {
			if err := b.CheckSecret("PAYRUDDER_X", v); err == nil || !strings.Contains(err.Error(), "PAYRUDDER_X") {
				t.Errorf("%s accepted %q: %v", e, v, err)
			}
		}
		for _, v := range []string{"", own} {
			if err := b.CheckSecret("PAYRUDDER_X", v); err != nil {
				t.Errorf("%s with %q: %v", e, v, err)
			}
		}
	}
}
