package membudget

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	budgetsPath = "../../deploy/compose/memory-budgets.yaml"
	composePath = "../../deploy/compose/compose.yaml"
)

func TestParseMiB(t *testing.T) {
	for in, want := range map[string]float64{
		"12.5MiB / 96MiB": 12.5,
		"1.5GiB / 2GiB":   1536,
		"512KiB / 1GiB":   0.5,
		"2097152B / 1GiB": 2,
		"0B / 0B":         0,
		"3MB / 1GB":       3e6 / (1 << 20),
		"48MiB":           48,
	} {
		got, err := ParseMiB(in)
		if err != nil || math.Abs(got-want) > 1e-6 {
			t.Errorf("ParseMiB(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "lots", "12 MiB MiB", "MiB"} {
		if _, err := ParseMiB(bad); err == nil {
			t.Errorf("ParseMiB(%q) should fail", bad)
		}
	}
}

func TestSuggest(t *testing.T) {
	for in, want := range map[float64]int{0: 16, 10: 32, 20: 40, 100: 144, 250: 352} {
		if got := Suggest(in); got != want {
			t.Errorf("Suggest(%v) = %d, want %d", in, got, want)
		}
	}
}

func TestCheck(t *testing.T) {
	f := File{Services: map[string]Budget{"a": {BudgetMiB: 40}, "b": {BudgetMiB: 40}, "c": {BudgetMiB: 40}}}
	rows := Check(f, map[string]float64{"a": 39.9, "b": 40.1})
	if !rows[0].OK() || rows[1].OK() || rows[2].OK() || rows[2].Running {
		t.Fatalf("rows = %+v: a is inside, b over, c missing", rows)
	}
	tbl := Table(rows)
	for _, want := range []string{"| a |", "OVER BUDGET", "NOT RUNNING"} {
		if !strings.Contains(tbl, want) {
			t.Errorf("table lacks %q:\n%s", want, tbl)
		}
	}
}

func TestLoadRejectsMistakes(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"typo":   "services:\n  gateway: {budget_mb: 40}\n",
		"zero":   "services:\n  gateway: {budget_mib: 0}\n",
		"empty":  "services: {}\n",
		"nested": "services:\n  gateway: 40\n",
	} {
		p := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Errorf("%s: Load accepted %q", name, content)
		}
	}
}

// Every service the compose file builds from this repository has a budget, and every budget names such a service, so that a new service cannot skip the
// check and a removed one does not leave a budget that can never be measured.
func TestBudgetsCoverEveryBuiltService(t *testing.T) {
	f, err := Load(budgetsPath)
	if err != nil {
		t.Fatal(err)
	}
	built, err := BuiltServices(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(built) == 0 {
		t.Fatal("no built services found in the compose file")
	}
	for _, s := range built {
		if _, ok := f.Services[s]; !ok {
			t.Errorf("compose service %q is built here but has no entry in %s", s, budgetsPath)
		}
	}
	for s := range f.Services {
		if !slices.Contains(built, s) {
			t.Errorf("%s budgets %q, which the compose file does not build", budgetsPath, s)
		}
	}
}
