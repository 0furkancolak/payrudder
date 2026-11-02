package main

import (
	"bytes"
	"encoding/base64"
	"maps"
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/config"
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

// The secrets key and its rotation settings reach the box from the environment (they sit in a nested struct of Config), and a mistake in them stops the start.
func TestSecretsSettingsComeFromTheEnvironment(t *testing.T) {
	key := func(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }
	base := map[string]string{
		"PAYRUDDER_SERVICE_NAME": "payments-worker", "PAYRUDDER_RABBITMQ_URL": "amqp://localhost", "PAYRUDDER_PG_SYSTEM_DSN": "postgres://localhost/payments_db",
	}
	load := func(extra map[string]string) (Config, error) {
		env := map[string]string{}
		maps.Copy(env, base)
		maps.Copy(env, extra)
		return config.LoadFrom[Config](env, defaults)
	}
	cfg, err := load(map[string]string{"PAYRUDDER_SECRETS_KEY": key(2), "PAYRUDDER_SECRETS_KEY_ID": "2", "PAYRUDDER_SECRETS_KEY_PREVIOUS": key(1)})
	if err != nil {
		t.Fatal(err)
	}
	box, err := cfg.Secrets.SecretBox()
	if err != nil || box == nil {
		t.Fatalf("box = %v, %v", box, err)
	}
	if sealed, _ := box.Seal([]byte("x"), nil); sealed[0] != 2 {
		t.Errorf("a box is sealed with key id %d, want 2", sealed[0])
	}
	if _, err := load(map[string]string{"PAYRUDDER_SECRETS_KEY": key(2), "PAYRUDDER_SECRETS_KEY_PREVIOUS": key(1)}); err == nil {
		t.Error("two keys with the same id (both 1) were accepted")
	}
}

// The secrets key and the token the gateway sends are refused outside a development environment while they are the values this repository publishes: the key would
// seal every tenant's provider credentials under a key anybody can read.
func TestPublishedSecretsAreRefusedOutsideDevelopment(t *testing.T) {
	const publicKey, publicToken = "bG9jYWwtZGV2LWtleS1jaGFuZ2UtbWUtMDEyMzQ1Njc=", "local-internal-token-change-me-0123456789"
	own := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	load := func(env string, kv ...string) error {
		environ := map[string]string{
			"PAYRUDDER_SERVICE_NAME": "payments-worker", "PAYRUDDER_RABBITMQ_URL": "amqp://localhost", "PAYRUDDER_PG_SYSTEM_DSN": "postgres://localhost/payments_db", "PAYRUDDER_ENV": env,
		}
		for i := 0; i < len(kv); i += 2 {
			environ[kv[i]] = kv[i+1]
		}
		_, err := config.LoadFrom[Config](environ, defaults)
		return err
	}
	for _, env := range []string{"local", "ci", "kind"} {
		if err := load(env, "PAYRUDDER_SECRETS_KEY", publicKey, "PAYMENTS_INTERNAL_TOKEN", publicToken); err != nil {
			t.Errorf("%s keeps the published values: %v", env, err)
		}
	}
	for _, env := range []string{"railway", "production"} {
		if err := load(env, "PAYRUDDER_SECRETS_KEY", own, "PAYMENTS_INTERNAL_TOKEN", strings.Repeat("k3Jx9mQ2", 4)); err != nil {
			t.Errorf("%s with its own values: %v", env, err)
		}
		if err := load(env); err != nil {
			t.Errorf("%s with no key and no token (features off): %v", env, err)
		}
		for name, kv := range map[string][]string{
			"PAYRUDDER_SECRETS_KEY":   {"PAYRUDDER_SECRETS_KEY", publicKey},
			"PAYMENTS_INTERNAL_TOKEN": {"PAYMENTS_INTERNAL_TOKEN", publicToken},
		} {
			if err := load(env, kv...); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%s with the published %s: %v", env, name, err)
			}
		}
	}
}
