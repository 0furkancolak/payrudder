package main

import (
	"bytes"
	"encoding/base64"
	"maps"
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
