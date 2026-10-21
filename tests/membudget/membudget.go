// Package membudget checks the memory of the running compose stack against budgets kept in deploy/compose/memory-budgets.yaml: after a smoke run, no Quotify
// service may use more memory than its budget. The numbers are a container's memory as `docker stats` reports it (the cgroup's usage without reclaimable page
// cache, so in practice the process's resident set), the same measure tests/bench uses.
package membudget

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Budget is one service's limit and the value it was set from.
type Budget struct {
	// BudgetMiB is the most memory the service may use in the smoke run.
	BudgetMiB float64 `yaml:"budget_mib"`
	// MeasuredMiB is what the run measured when the budget was last set. It is documentation: the check does not read it.
	MeasuredMiB float64 `yaml:"measured_mib"`
}

// File is deploy/compose/memory-budgets.yaml: budgets by compose service name.
type File struct {
	Services map[string]Budget `yaml:"services"`
}

// Load reads and validates a budget file. Unknown fields and non-positive budgets are errors, so a typo cannot disable a budget.
func Load(path string) (File, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a fixed repository path or one given by the developer
	if err != nil {
		return File{}, err
	}
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Services) == 0 {
		return File{}, fmt.Errorf("%s: no services", path)
	}
	for name, s := range f.Services {
		if s.BudgetMiB <= 0 {
			return File{}, fmt.Errorf("%s: service %q needs a budget_mib above zero", path, name)
		}
	}
	return f, nil
}

// ParseMiB reads the used part of a `docker stats` MemUsage cell ("12.5MiB / 96MiB") in MiB.
func ParseMiB(usage string) (float64, error) {
	used, _, _ := strings.Cut(usage, "/")
	used = strings.TrimSpace(used)
	units := []struct {
		suffix string
		toMiB  float64
	}{
		{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"GB", 1e9 / (1 << 20)}, {"MB", 1e6 / (1 << 20)}, {"kB", 1e3 / (1 << 20)}, {"KB", 1e3 / (1 << 20)}, {"B", 1.0 / (1 << 20)},
	}
	for _, u := range units {
		if num, ok := strings.CutSuffix(used, u.suffix); ok {
			n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil {
				return 0, fmt.Errorf("memory %q: %w", usage, err)
			}
			return n * u.toMiB, nil
		}
	}
	return 0, fmt.Errorf("memory %q: unknown unit", usage)
}

// Suggest is the budget to set for a measured value: 40 % headroom or 16 MiB, whichever is more, rounded up to a multiple of 8 MiB. Small services
// get the absolute margin (a 10 MiB service varies by more than 40 % of itself between runners), large ones the relative one.
func Suggest(measuredMiB float64) int {
	v := math.Max(measuredMiB*1.4, measuredMiB+16)
	return int(math.Ceil(v/8)) * 8
}

// Row is the verdict for one service.
type Row struct {
	Service     string
	MeasuredMiB float64
	BudgetMiB   float64
	// Running is false when the service has a budget but no container was found.
	Running bool
}

// OK reports whether the service is within its budget.
func (r Row) OK() bool { return r.Running && r.MeasuredMiB <= r.BudgetMiB }

// Check compares measurements (MiB by service) with the budgets. A budgeted service that was not measured is a failure: a budget for a service that is not
// running checks nothing. Rows are sorted by service name.
func Check(f File, measured map[string]float64) []Row {
	rows := make([]Row, 0, len(f.Services))
	for name, b := range f.Services {
		m, ok := measured[name]
		rows = append(rows, Row{Service: name, MeasuredMiB: m, BudgetMiB: b.BudgetMiB, Running: ok})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Service < rows[j].Service })
	return rows
}

// Table renders rows as a Markdown table with the budget to set for each service.
func Table(rows []Row) string {
	var sb strings.Builder
	sb.WriteString("| service | measured MiB | budget MiB | suggested budget MiB | |\n|---|---:|---:|---:|---|\n")
	for _, r := range rows {
		verdict := "ok"
		switch {
		case !r.Running:
			verdict = "NOT RUNNING"
		case !r.OK():
			verdict = "OVER BUDGET"
		}
		fmt.Fprintf(&sb, "| %s | %.1f | %.0f | %d | %s |\n", r.Service, r.MeasuredMiB, r.BudgetMiB, Suggest(r.MeasuredMiB), verdict)
	}
	return sb.String()
}

// BuiltServices lists the services of a compose file that are built from this repository, which are the Quotify services that need a budget.
func BuiltServices(composePath string) ([]string, error) {
	b, err := os.ReadFile(composePath) //nolint:gosec // a fixed repository path
	if err != nil {
		return nil, err
	}
	var c struct {
		Services map[string]struct {
			Build any `yaml:"build"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", composePath, err)
	}
	var out []string
	for name, s := range c.Services {
		if s.Build != nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}
