// Package alerts checks the alert rules against what the plans promise of them (plan 16): every rule has a promtool unit test in which it fires, and a place in the docs a
// person on call can find. promtool itself runs the tests (make test-rules); this reads the two files and the docs, so an alert added without its test or its first response
// fails here, in the unit tests, and not on the night it fires.
package alerts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type rulesFile struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert  string            `yaml:"alert"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

type testsFile struct {
	Tests []struct {
		Name string `yaml:"name"`
		Rule []struct {
			Alertname string           `yaml:"alertname"`
			Exp       []map[string]any `yaml:"exp_alerts"`
		} `yaml:"alert_rule_test"`
	} `yaml:"tests"`
}

func read(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestEveryAlertFiresInAUnitTestAndIsDocumented(t *testing.T) {
	root := filepath.Join("..", "..")
	var rules rulesFile
	var tests testsFile
	read(t, filepath.Join(root, "deploy", "prometheus", "rules", "payrudder.yaml"), &rules)
	read(t, filepath.Join(root, "deploy", "prometheus", "tests", "payrudder_test.yaml"), &tests)

	fires := map[string]bool{}
	for _, tc := range tests.Tests {
		for _, r := range tc.Rule {
			if len(r.Exp) > 0 {
				fires[r.Alertname] = true
			}
		}
	}
	// Where a person on call reads what an alert means and what to do first.
	var docs strings.Builder
	for _, f := range []string{filepath.Join("runbooks", "backup-restore.md"), "08-observability.md"} {
		raw, err := os.ReadFile(filepath.Join(root, "docs", f))
		if err != nil {
			t.Fatal(err)
		}
		docs.Write(raw)
	}
	seen := 0
	for _, g := range rules.Groups {
		for _, r := range g.Rules {
			if r.Alert == "" {
				continue
			}
			seen++
			if !fires[r.Alert] {
				t.Errorf("%s (group %s) has no promtool test in which it fires (deploy/prometheus/tests/payrudder_test.yaml)", r.Alert, g.Name)
			}
			if !strings.Contains(docs.String(), "`"+r.Alert+"`") {
				t.Errorf("%s is not in docs/runbooks/backup-restore.md or docs/08-observability.md", r.Alert)
			}
			if sev := r.Labels["severity"]; sev != "page" && sev != "warn" && sev != "none" {
				t.Errorf("%s has severity %q: Alertmanager routes page, warn and the watchdog's none", r.Alert, sev)
			}
		}
	}
	if seen < 30 {
		t.Errorf("read %d alerts: the rules file changed shape?", seen)
	}
	for name := range fires {
		found := false
		for _, g := range rules.Groups {
			for _, r := range g.Rules {
				found = found || r.Alert == name
			}
		}
		if !found {
			t.Errorf("a promtool test fires %s, which no rule defines", name)
		}
	}
}
