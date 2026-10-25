package tests

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR 0022: what people read says Payrudder. This fails on "Quotify" in the console's locale files and in the e-mail and hosted-page templates. Add a
// string to brandAllowed only for a legal or compatibility note that must name the old product (none today).
var brandAllowed = []string{}

var brandRoots = []string{
	"web/admin/src/locales",
	"services/notifier/internal/templates/files",
	"services/plans-svc/internal/identity/usermail",
	"services/gateway/internal/httpapi/hosted_templates.go",
}

func TestNoQuotifyInUserVisibleText(t *testing.T) {
	root := filepath.Join("..")
	for _, r := range brandRoots {
		err := filepath.WalkDir(filepath.Join(root, r), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, ".test.ts") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				for _, ok := range brandAllowed {
					line = strings.ReplaceAll(line, ok, "")
				}
				if strings.Contains(line, "Quotify") && !strings.HasPrefix(strings.TrimSpace(line), "//") {
					t.Errorf("%s:%d says Quotify; user-visible text says Payrudder (ADR 0022): %s", p, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("%s: %v", r, err)
		}
	}
}
