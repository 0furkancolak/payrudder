package main

import (
	"strings"
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

// PAYRUDDER_REGION is off by default; set, it needs a region name, and the addresses of the other regions' gateways have to be http(s) addresses.
func TestRegionSettings(t *testing.T) {
	load := func(kv ...string) (Config, error) {
		env := map[string]string{"PAYRUDDER_SERVICE_NAME": "gateway", "PAYRUDDER_OPERATOR_TOKEN": strings.Repeat("t", 32)}
		for i := 0; i < len(kv); i += 2 {
			env[kv[i]] = kv[i+1]
		}
		return config.LoadFrom[Config](env, defaults)
	}
	c, err := load()
	if err != nil || c.Region != "" || len(c.RegionURLs) != 0 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c, err = load("PAYRUDDER_REGION", "us", "PAYRUDDER_REGION_URLS", "eu=https://eu.api.example.com,us=https://us.api.example.com")
	if err != nil || c.Region != "us" || c.RegionURLs["eu"] != "https://eu.api.example.com" || len(c.RegionURLs) != 2 {
		t.Fatalf("a region and its addresses: %+v %v", c, err)
	}
	for _, kv := range [][]string{
		{"PAYRUDDER_REGION", "EU"},
		{"PAYRUDDER_REGION", "-eu"},
		{"PAYRUDDER_REGION_URLS", "eu=api.example.com"},
		{"PAYRUDDER_REGION_URLS", "EU=https://eu.api.example.com"},
		{"PAYRUDDER_REGION_URLS", "eu=ftp://eu.api.example.com"},
	} {
		if _, err := load(kv...); err == nil {
			t.Errorf("accepted %v", kv)
		}
	}
}

// Operators must use two-factor sign-in wherever the deployment is not a development stack: PAYRUDDER_OPERATOR_REQUIRE_2FA decides, and unset it follows PAYRUDDER_ENV
// (off for local, ci and kind, which are the dev, test and kind installs; on for anything else).
func TestOperatorTwoFactorSetting(t *testing.T) {
	load := func(kv ...string) (Config, error) {
		env := map[string]string{"PAYRUDDER_SERVICE_NAME": "gateway", "PAYRUDDER_OPERATOR_TOKEN": strings.Repeat("t", 32)}
		for i := 0; i < len(kv); i += 2 {
			env[kv[i]] = kv[i+1]
		}
		return config.LoadFrom[Config](env, defaults)
	}
	for _, tc := range []struct {
		env, setting string
		want         bool
	}{
		{"local", "", false}, {"ci", "", false}, {"kind", "", false}, {"railway", "", true},
		{"local", "true", true}, {"railway", "false", false}, {"railway", "off", false}, {"kind", "on", true},
	} {
		c, err := load("PAYRUDDER_ENV", tc.env, "PAYRUDDER_OPERATOR_REQUIRE_2FA", tc.setting)
		if err != nil || c.operatorRequireTwoFactor() != tc.want {
			t.Errorf("env %q setting %q: %v %v, want %v", tc.env, tc.setting, c.operatorRequireTwoFactor(), err, tc.want)
		}
	}
	if _, err := load("PAYRUDDER_OPERATOR_REQUIRE_2FA", "sometimes"); err == nil {
		t.Error("a typo must not leave the requirement on or off by accident")
	}
}

// The provider's delivery webhook is off by default; its secret has to be the form Svix signs with (whsec_ and base64), and it needs the notifier to hand the events to.
func TestMailWebhookSecretSetting(t *testing.T) {
	load := func(kv ...string) error {
		env := map[string]string{"PAYRUDDER_SERVICE_NAME": "gateway", "PAYRUDDER_OPERATOR_TOKEN": strings.Repeat("t", 32)}
		for i := 0; i < len(kv); i += 2 {
			env[kv[i]] = kv[i+1]
		}
		_, err := config.LoadFrom[Config](env, defaults)
		return err
	}
	notifier := []string{"PAYRUDDER_NOTIFIER_EVENTS_URL", "http://notifier:9111", "PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET", strings.Repeat("s", 32)}
	secret := "whsec_dGhlLXN2aXgtc2VjcmV0LW9mLXRoZS10ZXN0cw=="
	if err := load(); err != nil {
		t.Fatalf("off by default: %v", err)
	}
	if err := load(append(notifier, "PAYRUDDER_MAIL_WEBHOOK_SECRET", secret)...); err != nil {
		t.Fatalf("a secret and the notifier: %v", err)
	}
	for name, kv := range map[string][]string{
		"no notifier to hand events to": {"PAYRUDDER_MAIL_WEBHOOK_SECRET", secret},
		"not a Svix secret":             append(append([]string{}, notifier...), "PAYRUDDER_MAIL_WEBHOOK_SECRET", "just-a-string"),
	} {
		if load(kv...) == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
