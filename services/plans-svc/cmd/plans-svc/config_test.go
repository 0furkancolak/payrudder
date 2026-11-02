package main

import (
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

// The pepper that keys the hash of sign-in and recovery codes is optional in a development environment and nowhere else; the values this repository publishes do not
// count as one.
func TestOTPPepperIsRequiredOutsideDevelopment(t *testing.T) {
	good := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	load := func(env, pepper string) error {
		environ := map[string]string{"PAYRUDDER_SERVICE_NAME": "plans-svc", "PAYRUDDER_ENV": env, "PAYRUDDER_PG_APP_DSN": "postgres://a", "PAYRUDDER_PG_SYSTEM_DSN": "postgres://s"}
		if pepper != "" {
			environ["PAYRUDDER_OTP_PEPPER"] = pepper
		}
		_, err := config.LoadFrom[Config](environ, defaults)
		return err
	}
	for _, env := range []string{"local", "ci", "kind"} {
		for _, pepper := range []string{"", "short", publicPeppers[0], good} {
			if err := load(env, pepper); err != nil {
				t.Errorf("%s with pepper %q: %v", env, pepper, err)
			}
		}
	}
	for _, env := range []string{"railway", "production"} {
		if err := load(env, good); err != nil {
			t.Errorf("%s with a good pepper: %v", env, err)
		}
		for name, pepper := range map[string]string{"none": "", "short": "0123456789abcdef", "the compose default": publicPeppers[0], "the secrets example": publicPeppers[1],
			"31 characters": good[:31]} {
			err := load(env, pepper)
			if err == nil || !strings.Contains(err.Error(), "PAYRUDDER_OTP_PEPPER") {
				t.Errorf("%s with %s: %v", env, name, err)
			}
		}
	}
}

// The Helm migration Job runs the service's image with PAYRUDDER_MIGRATE_ONLY and exits before it issues a code. With secrets.existingSecrets the pepper is in the
// operator's own Secret, which the Job does not read, so a migrate-only run must start without one even where a serving pod needs it.
func TestMigrateOnlyRunNeedsNoPepper(t *testing.T) {
	environ := func(migrateOnly string) map[string]string {
		m := map[string]string{"PAYRUDDER_SERVICE_NAME": "plans-svc", "PAYRUDDER_ENV": "production", "PAYRUDDER_PG_APP_DSN": "postgres://a", "PAYRUDDER_PG_SYSTEM_DSN": "postgres://s",
			"PAYRUDDER_PG_MIGRATOR_DSN": "postgres://m", "PAYRUDDER_MIGRATE_ON_START": "true"}
		if migrateOnly != "" {
			m["PAYRUDDER_MIGRATE_ONLY"] = migrateOnly
		}
		return m
	}
	for _, pepper := range []string{"", "short", publicPeppers[0]} {
		with := environ("true")
		with["PAYRUDDER_OTP_PEPPER"] = pepper
		if _, err := config.LoadFrom[Config](with, defaults); err != nil {
			t.Errorf("migrate-only with pepper %q: %v", pepper, err)
		}
	}
	if _, err := config.LoadFrom[Config](environ(""), defaults); err == nil || !strings.Contains(err.Error(), "PAYRUDDER_OTP_PEPPER") {
		t.Errorf("a serving pod without a pepper started: %v", err)
	}
}

// The secrets key and the token to payments-worker are refused outside a development environment while they are the values this repository publishes (plans-svc seals
// webhook secrets, mail keys and two-factor secrets under the key).
func TestPublishedSecretsAreRefusedOutsideDevelopment(t *testing.T) {
	const publicKey, publicToken = "bG9jYWwtZGV2LWtleS1jaGFuZ2UtbWUtMDEyMzQ1Njc=", "local-internal-token-change-me-0123456789"
	good := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	load := func(env string, kv ...string) error {
		environ := map[string]string{"PAYRUDDER_SERVICE_NAME": "plans-svc", "PAYRUDDER_ENV": env, "PAYRUDDER_PG_APP_DSN": "postgres://a", "PAYRUDDER_PG_SYSTEM_DSN": "postgres://s", "PAYRUDDER_OTP_PEPPER": good}
		for i := 0; i < len(kv); i += 2 {
			environ[kv[i]] = kv[i+1]
		}
		_, err := config.LoadFrom[Config](environ, defaults)
		return err
	}
	for _, env := range []string{"local", "ci", "kind"} {
		if err := load(env, "PAYRUDDER_SECRETS_KEY", publicKey, "PAYRUDDER_PAYMENTS_INTERNAL_TOKEN", publicToken); err != nil {
			t.Errorf("%s keeps the published values: %v", env, err)
		}
	}
	for _, env := range []string{"railway", "production"} {
		for name, kv := range map[string][]string{
			"PAYRUDDER_SECRETS_KEY":             {"PAYRUDDER_SECRETS_KEY", publicKey},
			"PAYRUDDER_PAYMENTS_INTERNAL_TOKEN": {"PAYRUDDER_PAYMENTS_INTERNAL_TOKEN", publicToken},
		} {
			if err := load(env, kv...); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%s with the published %s: %v", env, name, err)
			}
		}
	}
}

// The limits on a flood of support tickets have a default, switch off at 0 and are never negative.
func TestSupportLimitsDefaultAndValidate(t *testing.T) {
	load := func(kv ...string) (Config, error) {
		environ := map[string]string{"PAYRUDDER_SERVICE_NAME": "plans-svc", "PAYRUDDER_PG_APP_DSN": "postgres://a", "PAYRUDDER_PG_SYSTEM_DSN": "postgres://s"}
		for i := 0; i < len(kv); i += 2 {
			environ[kv[i]] = kv[i+1]
		}
		return config.LoadFrom[Config](environ, defaults)
	}
	c, err := load()
	if err != nil || c.SupportOpensPerHour != 10 || c.SupportMailBurst != 5 {
		t.Fatalf("defaults: %d, %d, %v", c.SupportOpensPerHour, c.SupportMailBurst, err)
	}
	if _, err := load("PAYRUDDER_SUPPORT_OPENS_PER_HOUR", "0", "PAYRUDDER_SUPPORT_MAIL_BURST", "0"); err != nil {
		t.Errorf("0 switches them off: %v", err)
	}
	for _, kv := range [][]string{{"PAYRUDDER_SUPPORT_OPENS_PER_HOUR", "-1"}, {"PAYRUDDER_SUPPORT_MAIL_BURST", "-1"}} {
		if _, err := load(kv...); err == nil {
			t.Errorf("accepted %v", kv)
		}
	}
}
