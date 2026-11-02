package config

import (
	"fmt"
	"strings"
)

// publishedSecretsKey is the secrets key of the compose stack and the Helm chart: base64 of "local-dev-key-change-me-01234567". Every other value this repository
// publishes (the compose and chart defaults, the placeholders of values-secrets.example.yaml) says "change-me" in it.
const publishedSecretsKey = "bG9jYWwtZGV2LWtleS1jaGFuZ2UtbWUtMDEyMzQ1Njc="

// CheckSecret refuses a secret that is a value this repository publishes (a default of the compose stack or the Helm chart, a placeholder of the secrets example)
// unless this is a development environment: anybody can read those, so they protect nothing. An empty value passes; whether it is allowed is the caller's business.
func (b Base) CheckSecret(name, value string) error {
	if b.Env.Development() || (value != publishedSecretsKey && !strings.Contains(strings.ToLower(value), "change-me")) {
		return nil
	}
	return fmt.Errorf("%s is a value this repository publishes, so it protects nothing: generate your own with `openssl rand -base64 32` (a development environment, PAYRUDDER_ENV local, ci or kind, may keep it)", name)
}
