package helm

import (
	"strings"
	"testing"
)

// ownSecrets are the values a production profile gets from its secrets file (values-secrets.example.yaml): without them the chart refuses to render there.
func ownSecrets() []string {
	return []string{
		"--set", "auth.otpPepper=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"--set", "operatorToken=k3Jx9mQ2vLw8Rt5Yh7Ud1Zn4Bc6Pf0Sa",
		"--set", "payments.secretsKey=k3Jx9mQ2vLw8Rt5Yh7Ud1Zn4Bc6Pf0SaGeIoVxTqMuc=",
		"--set", "payments.internalToken=Yh7Ud1Zn4Bc6Pf0Sak3Jx9mQ2vLw8Rt5",
	}
}

// productionValues is ownSecrets and the public address, the other thing a non-development environment must set (TestPublicURLIsRequiredOutsideDevelopment).
func productionValues() []string { return append(ownSecrets(), "--set", "global.publicURL="+publicURL) }

// The values this chart ships (and the placeholders of the secrets example) are public, so outside a development environment the chart does not render with one:
// the operator token alone opens /admin/v1/*, the secrets key seals every tenant's provider credentials, the internal token opens payments-worker's /admin, and the
// signing secret of the mail listener lets its caller suppress a tenant's mail. The services refuse the same values at startup (pkg/config CheckSecret); this
// catches it before the install.
func TestProductionRefusesThePublishedSecrets(t *testing.T) {
	profiles := map[string][]string{
		"production":   {"--set", "global.environment=production"},
		"railway":      {"--set", "global.environment=railway"},
		"values-small": {"-f", values(t, "payrudder", "values-small.yaml")},
	}
	for name, base := range profiles {
		if out, err := tryRender("payrudder", append(append([]string{}, base...), productionValues()...)...); err != nil {
			t.Errorf("%s with its own secrets must render: %v\n%s", name, err, out)
		}
		for _, tc := range []struct{ key, public string }{
			{"operatorToken", "local-operator-token-change-me-0123456789"},
			{"payments.secretsKey", "bG9jYWwtZGV2LWtleS1jaGFuZ2UtbWUtMDEyMzQ1Njc="},
			{"payments.internalToken", "local-internal-token-change-me-0123456789"},
			{"mail.eventsSecret", "local-mail-events-secret-change-me-0123456789"},
			{"operatorToken", "CHANGE-ME-at-least-32-characters-long-xxxxxxxxxxxx"},
			{"payments.secretsKey", "CHANGE-ME-32-bytes-base64"},
			{"payments.internalToken", "CHANGE-ME-32-characters-or-more-xxxxxxxxx"},
			{"mail.eventsSecret", "CHANGE-ME-16-characters-or-more-xxxxxxxxx"},
			{"operatorToken", ""},
			{"payments.secretsKey", ""},
			{"payments.internalToken", ""},
		} {
			args := append(append(append([]string{}, base...), productionValues()...), "--set", tc.key+"="+tc.public)
			if out, err := tryRender("payrudder", args...); err == nil || !strings.Contains(out, tc.key) || !strings.Contains(out, "your own") {
				t.Errorf("%s with %s=%q rendered or failed for another reason: %v\n%s", name, tc.key, tc.public, err, out)
			}
		}
		// The mail listener is optional: empty is off at both ends, and one of its own is wired.
		if out, err := tryRender("payrudder", append(append([]string{}, base...), productionValues()...)...); err != nil {
			t.Errorf("%s with no mail listener: %v\n%s", name, err, out)
		}
		own := "Yh7Ud1Zn4Bc6Pf0Sak3Jx9mQ2vLw8Rt5"
		if out, err := tryRender("payrudder", append(append(append([]string{}, base...), productionValues()...), "--set", "mail.eventsSecret="+own)...); err != nil {
			t.Errorf("%s with a mail secret of its own: %v\n%s", name, err, out)
		}
	}
	// The Secrets are the operator's own with existingSecrets: nothing is rendered from the values, so nothing is checked here (the services check at startup).
	if out, err := tryRender("payrudder", "--set", "global.environment=production", "--set", "global.publicURL="+publicURL, "--set", "secrets.existingSecrets=true"); err != nil {
		t.Errorf("production with existingSecrets must render: %v\n%s", err, out)
	}
	// A development environment keeps the local values, but the mail listener is off until a profile or a value turns it on.
	for _, e := range []string{"local", "ci", "kind"} {
		if out, err := tryRender("payrudder", "--set", "global.environment="+e); err != nil {
			t.Errorf("%s with the local values must render: %v\n%s", e, err, out)
		}
	}
}
