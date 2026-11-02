// Package ci tests .github/scripts/changes.sh, which decides which jobs of ci.yml a change has to run. A wrong "false" here is a job that never ran on a change that
// needed it, so the cases are the folders whose files other jobs read.
package ci

import (
	"os/exec"
	"strings"
	"testing"
)

func decide(t *testing.T, files ...string) map[string]string {
	t.Helper()
	cmd := exec.Command("sh", "../../.github/scripts/changes.sh")
	cmd.Stdin = strings.NewReader(strings.Join(files, "\n"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("changes.sh: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			t.Fatalf("not key=value: %q", l)
		}
		got[k] = v
	}
	return got
}

func TestWhichJobsRun(t *testing.T) {
	cases := []struct {
		name          string
		files         []string
		goJobs, webCI string
	}{
		{"a plan only", []string{"docs/plans/01-baseline-and-review.md"}, "false", "false"},
		{"plans and the readme", []string{"docs/plans/README.md", "README.md"}, "false", "false"},
		// Go tests read these
		{"a doc a Go test reads", []string{"docs/email-template-contract.md"}, "true", "false"},
		{"a railway readme", []string{"deploy/railway/README.md"}, "true", "false"},
		{"console locales (pkg/errkey reads them)", []string{"web/admin/src/locales/tr.json"}, "true", "true"},
		{"a service", []string{"services/billing-svc/internal/x.go"}, "true", "false"},
		{"the openapi file", []string{"api/openapi/v1.yaml"}, "true", "true"},
		{"an sdk with a workflow of its own", []string{"sdk/typescript/src/index.ts"}, "false", "false"},
		{"mcp, examples and the docs site", []string{"mcp/src/a.ts", "examples/express/a.js", "docs-site/content/x.md"}, "false", "false"},
		{"the Go sdk is Go", []string{"sdk/go/client.go"}, "true", "false"},
		{"a workflow", []string{".github/workflows/ci.yml"}, "true", "true"},
		{"one Go file hides among plans", []string{"docs/plans/a.md", "pkg/money/money.go"}, "true", "false"},
		{"a path nobody listed", []string{"somewhere/new.txt"}, "true", "false"},
		{"nothing known runs everything", nil, "true", "true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decide(t, c.files...)
			if got["go"] != c.goJobs || got["web"] != c.webCI {
				t.Errorf("go/web = %s/%s, want %s/%s", got["go"], got["web"], c.goJobs, c.webCI)
			}
		})
	}
}
