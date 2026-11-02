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
