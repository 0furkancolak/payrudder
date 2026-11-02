package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// composeEnv returns the environment of every service of the local stack, as the compose file spells it (values before ${...} interpolation).
func composeEnv(t *testing.T) map[string]map[string]string {
	env, _ := composeStack(t)
	return env
}

// composeStack returns the environment and the published ports of every service.
func composeStack(t *testing.T) (env map[string]map[string]string, ports map[string][]string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "deploy", "compose", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Services map[string]struct {
			Environment map[string]any `yaml:"environment"`
			Ports       []string       `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	env, ports = map[string]map[string]string{}, map[string][]string{}
	for name, s := range c.Services {
		env[name], ports[name] = map[string]string{}, s.Ports
		for k, v := range s.Environment {
			env[name][k], _ = v.(string)
		}
	}
	return env, ports
}

// The console's mail page (plan 11) is "not connected" unless the notifier listens for its signed requests and the gateway calls it with the same secret. The compose
// stack wires both; the services refuse one end without the other, so the two secrets are one setting.
func TestComposeWiresTheNotifiersMailEventsListenerToTheGateway(t *testing.T) {
	env, ports := composeStack(t)
	// make dev runs a gateway on the host: it finds the listener on the published port, and only this machine may (the secret is a local default).
	if len(ports["notifier"]) != 1 || ports["notifier"][0] != "127.0.0.1:42920:9111" {
		t.Errorf("the notifier publishes %v, want only 127.0.0.1:42920:9111", ports["notifier"])
	}
	addr := env["notifier"]["NOTIFIER_EVENTS_ADDR"]
	if addr != ":9111" {
		t.Errorf("the notifier's NOTIFIER_EVENTS_ADDR = %q, want :9111", addr)
	}
	if got := env["gateway"]["PAYRUDDER_NOTIFIER_EVENTS_URL"]; got != "http://notifier"+addr {
		t.Errorf("the gateway's PAYRUDDER_NOTIFIER_EVENTS_URL = %q, want http://notifier%s", got, addr)
	}
	n, g := env["notifier"]["NOTIFIER_MAIL_EVENTS_SECRET"], env["gateway"]["PAYRUDDER_NOTIFIER_MAIL_EVENTS_SECRET"]
	if n == "" || n != g {
		t.Errorf("the notifier checks %q and the gateway signs with %q: they must be the same, and set", n, g)
	}
}

// PAYRUDDER_ERROR_TRACKING_URL (docs/08-observability.md) reaches every Go service of the stack when the operator sets it in .env, and is empty otherwise.
func TestComposePassesTheErrorTrackingURLToEveryGoService(t *testing.T) {
	const want = "${PAYRUDDER_ERROR_TRACKING_URL:-}"
	env := composeEnv(t)
	var checked int
	for name, e := range env {
		if _, goService := e["PAYRUDDER_OTLP_ENDPOINT"]; !goService || strings.HasPrefix(name, "mock") {
			continue // infrastructure and the development stand-ins have no tracker
		}
		if e["PAYRUDDER_SERVICE_NAME"] == "" {
			continue
		}
		checked++
		if e["PAYRUDDER_ERROR_TRACKING_URL"] != want {
			t.Errorf("%s: PAYRUDDER_ERROR_TRACKING_URL = %q, want %q", name, e["PAYRUDDER_ERROR_TRACKING_URL"], want)
		}
	}
	if checked < 11 {
		t.Errorf("only %d Go services checked: the service filter no longer matches the compose file", checked)
	}
}
