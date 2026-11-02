package templates

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/mailcategory"
)

var (
	indexVar = regexp.MustCompile(`index \.D "(\w+)"`)
	fieldVar = regexp.MustCompile(`\.D\.(\w+)`)
	subjVar  = regexp.MustCompile(`\{\{\.(\w+)\}\}`)
	row      = regexp.MustCompile("^\\| `([a-z_]+\\.v\\d+)` \\|(.*)\\|\\s*$")
	ticked   = regexp.MustCompile("`(\\w+)`")
)

// read reports the variables every language file and subject of each template reads.
func readVariables(t *testing.T) map[string][]string {
	t.Helper()
	sets := map[string]map[string]bool{}
	add := func(name string, vs ...string) {
		if sets[name] == nil {
			sets[name] = map[string]bool{}
		}
		for _, v := range vs {
			sets[name][v] = true
		}
	}
	entries, err := files.ReadDir("files")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := files.ReadFile("files/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(e.Name(), ".html")
		for _, l := range Locales[1:] {
			name = strings.TrimSuffix(name, "."+l)
		}
		for _, re := range []*regexp.Regexp{indexVar, fieldVar} {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				add(name, m[1])
			}
		}
	}
	subjects := []map[string]string{subjects}
	for _, l := range localeSubjects {
		subjects = append(subjects, l)
	}
	for _, s := range subjects {
		for name, text := range s {
			for _, m := range subjVar.FindAllStringSubmatch(text, -1) {
				add(name, m[1])
			}
		}
	}
	out := map[string][]string{}
	for name, set := range sets {
		for v := range set {
			out[name] = append(out[name], v)
		}
		sort.Strings(out[name])
	}
	return out
}

// docs/email-template-contract.md lists, for every template, the variables it reads. The page is what Nafru's side would implement, so it must be the truth.
func TestContractDocMatchesTheTemplates(t *testing.T) {
	b, err := os.ReadFile("../../../../docs/email-template-contract.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string][]string{}
	categories := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		m := row.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		cols := strings.Split(m[2], "|") // category | sent when | variables
		if len(cols) < 3 {
			t.Fatalf("a template row needs a category, a trigger and variables: %q", line)
		}
		var vars []string
		for _, v := range ticked.FindAllStringSubmatch(cols[len(cols)-1], -1) {
			vars = append(vars, v[1])
		}
		sort.Strings(vars)
		documented[m[1]] = vars
		categories[m[1]] = strings.Trim(strings.TrimSpace(cols[0]), "`")
	}
	actual := readVariables(t)
	names := Names()
	if len(documented) != len(names) {
		t.Errorf("the contract lists %d templates, the notifier has %d (%v)", len(documented), len(names), names)
	}
	for _, name := range names {
		if categories[name] != mailcategory.ForTemplate(name) {
			t.Errorf("%s: the contract says category %q, mailcategory says %q", name, categories[name], mailcategory.ForTemplate(name))
		}
		if got, want := strings.Join(documented[name], ","), strings.Join(actual[name], ","); got != want {
			t.Errorf("%s: the contract says [%s], the files and subjects read [%s]", name, got, want)
		}
	}
}
