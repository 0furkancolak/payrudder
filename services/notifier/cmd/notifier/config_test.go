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

// The signing secret of the events listener is refused outside a development environment while it is the value this repository publishes: with it anybody who reaches
// the port can suppress a tenant's mail.
func TestPublishedMailEventsSecretIsRefusedOutsideDevelopment(t *testing.T) {
	load := func(env, secret string) error {
		environ := map[string]string{
			"PAYRUDDER_SERVICE_NAME": "notifier", "PAYRUDDER_RABBITMQ_URL": "amqp://localhost", "PAYRUDDER_KAFKA_BROKERS": "k:9092", "PAYRUDDER_PG_SYSTEM_DSN": "postgres://localhost/notifier_db",
			"PAYRUDDER_ENV": env, "NOTIFIER_EVENTS_ADDR": ":9111", "NOTIFIER_MAIL_EVENTS_SECRET": secret,
		}
		_, err := config.LoadFrom[Config](environ, defaults)
		return err
	}
	const public = "local-mail-events-secret-change-me-0123456789"
	for _, env := range []string{"local", "ci", "kind"} {
		if err := load(env, public); err != nil {
			t.Errorf("%s keeps the published secret: %v", env, err)
		}
	}
	for _, env := range []string{"railway", "production"} {
		if err := load(env, strings.Repeat("k3Jx9mQ2", 4)); err != nil {
			t.Errorf("%s with its own secret: %v", env, err)
		}
		if err := load(env, public); err == nil || !strings.Contains(err.Error(), "NOTIFIER_MAIL_EVENTS_SECRET") {
			t.Errorf("%s with the published secret: %v", env, err)
		}
	}
}
