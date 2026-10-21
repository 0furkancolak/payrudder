//go:build membudget

package membudget

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestStackStaysInBudget measures every container of the running compose stack (make up, after a smoke run such as make e2e) and fails when a service uses
// more than its budget. It samples a few times and takes each service's highest value, because a budget is about the peak the run showed.
func TestStackStaysInBudget(t *testing.T) {
	f, err := Load(budgetsPath)
	if err != nil {
		t.Fatal(err)
	}
	project := os.Getenv("QUOTIFY_COMPOSE_PROJECT")
	if project == "" {
		project = "quotify"
	}
	services := containersByService(t, project)
	if len(services) == 0 {
		t.Fatalf("no running containers in compose project %q: start the stack first (make up)", project)
	}

	measured := map[string]float64{}
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		for service, mib := range sample(t, services) {
			measured[service] = max(measured[service], mib)
		}
	}

	rows := Check(f, measured)
	table := Table(rows)
	t.Log("\n" + table)
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		if w, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600); err == nil { //nolint:gosec // GitHub's own summary file
			_, _ = fmt.Fprintf(w, "### Memory budgets\n\n%s\n", table)
			_ = w.Close()
		}
	}
	for _, r := range rows {
		switch {
		case !r.Running:
			t.Errorf("%s has a budget but no running container: is the stack up with the app profile?", r.Service)
		case !r.OK():
			t.Errorf("%s uses %.1f MiB, over its budget of %.0f MiB. If the growth is expected, raise budget_mib in deploy/compose/memory-budgets.yaml (suggested: %d); if not, find the leak or the new allocation", r.Service, r.MeasuredMiB, r.BudgetMiB, Suggest(r.MeasuredMiB))
		}
	}
}

// containersByService maps container ids to their compose service.
func containersByService(t *testing.T, project string) map[string]string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--filter", "label=com.docker.compose.project="+project,
		"--format", `{{.ID}}	{{.Label "com.docker.compose.service"}}`).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	ids := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, service, ok := strings.Cut(line, "\t")
		if ok && id != "" {
			ids[id] = service
		}
	}
	return ids
}

// sample reads the memory of the containers once and returns the highest value per service (a service may run as several replicas).
func sample(t *testing.T, services map[string]string) map[string]float64 {
	t.Helper()
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.ID}}\t{{.MemUsage}}").Output()
	if err != nil {
		t.Fatalf("docker stats: %v", err)
	}
	res := map[string]float64{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, usage, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		service, mine := services[id]
		if !mine {
			continue
		}
		mib, err := ParseMiB(usage)
		if err != nil {
			t.Fatalf("%s: %v", service, err)
		}
		res[service] = max(res[service], mib)
	}
	return res
}
