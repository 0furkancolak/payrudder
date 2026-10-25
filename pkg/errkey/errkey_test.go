package errkey

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestKeyTravelsWithTheStatus(t *testing.T) {
	err := New(codes.FailedPrecondition, "billing.credit_note.not_paid", "only a paid invoice can be credited")
	if got := Of(err); got != "billing.credit_note.not_paid" {
		t.Fatalf("key = %q", got)
	}
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "only a paid invoice can be credited" {
		t.Fatalf("status = %v", err)
	}
	if Of(fmt.Errorf("wrapped: %w", err)) == "" {
		t.Error("a wrapped error lost its key")
	}
	if Of(errors.New("plain")) != "" || Of(status.Error(codes.NotFound, "x")) != "" {
		t.Error("an error without a key has one")
	}
	if Default(codes.NotFound) != "common.not_found" || Default(codes.DataLoss) != "common.internal" {
		t.Error("default keys")
	}
}

var keyRe = regexp.MustCompile(`errkey\.(?:Newf?\(\s*codes\.\w+,|Key\()\s*"([a-z0-9_]+(?:\.[a-z0-9_]+)+)"`)

// defaultRe finds the keys errkey.Default hands to an error without one: they reach clients too, so they are listed and translated like any other.
var defaultRe = regexp.MustCompile(`return "(common\.[a-z0-9_]+)"`)
var shapeRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+){1,3}$`)

// TestEveryKeyIsListed finds the keys the code returns and compares them with docs/error-keys.md, so that the list a client developer reads is the list the
// code has. Regenerate it with: go test ./errkey -run TestEveryKeyIsListed -update-keys
func TestEveryKeyIsListed(t *testing.T) {
	root := "../.."
	found := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "gen") {
			if err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "pkg/errkey/") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, m := range keyRe.FindAllStringSubmatch(string(b), -1) {
			found[m[1]] = true
		}
		return nil
	})
	src, err := os.ReadFile("errkey.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range defaultRe.FindAllStringSubmatch(string(src), -1) {
		found[m[1]] = true
	}
	var keys []string
	for k := range found {
		if !shapeRe.MatchString(k) {
			t.Errorf("key %q is not service.thing.reason", k)
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := "# Error keys\n\nGenerated from the code by `go test ./pkg/errkey` (see ADR 0020). The key is in `error.key` of every API error; translate keys, not messages.\n\n"
	for _, k := range keys {
		want += "- `" + k + "`\n"
	}
	path := filepath.Join(root, "docs", "error-keys.md")
	if os.Getenv("UPDATE_ERROR_KEYS") != "" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil { //nolint:gosec // a docs file
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("docs/error-keys.md is out of date: run UPDATE_ERROR_KEYS=1 go test ./pkg/errkey (%v)", err)
	}
}

// TestEveryDefaultIsListed checks that each code Default can return is in docs/error-keys.md.
func TestEveryDefaultIsListed(t *testing.T) {
	doc, err := os.ReadFile("../../docs/error-keys.md")
	if err != nil {
		t.Fatal(err)
	}
	for c := codes.Code(0); c <= codes.Unauthenticated; c++ {
		if k := Default(c); !strings.Contains(string(doc), "- `"+k+"`\n") {
			t.Errorf("Default(%v) = %q is not in docs/error-keys.md", c, k)
		}
	}
}

// TestEveryKeyIsTranslated reads the console's locale files and fails when a listed key has no text in one of the six languages, so a new key cannot ship
// untranslated. web/admin/src/shared/lib/errors.test.ts checks the same from the other side (and that no stale text stays).
func TestEveryKeyIsTranslated(t *testing.T) {
	doc, err := os.ReadFile("../../docs/error-keys.md")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, m := range regexp.MustCompile("(?m)^- `([^`]+)`$").FindAllStringSubmatch(string(doc), -1) {
		keys = append(keys, m[1])
	}
	if len(keys) < 20 {
		t.Fatalf("found only %d keys in docs/error-keys.md", len(keys))
	}
	for _, lang := range []string{"en", "tr", "de", "fr", "es", "ar"} {
		raw, err := os.ReadFile(filepath.Join("../../web/admin/src/locales", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Errors map[string]any `json:"errors"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			var node any = file.Errors
			for _, part := range strings.Split(k, ".") {
				m, ok := node.(map[string]any)
				if !ok {
					node = nil
					break
				}
				node = m[part]
			}
			if s, ok := node.(string); !ok || s == "" {
				t.Errorf("locale %s has no translation for %s (add it under \"errors\" in web/admin/src/locales/%s.json)", lang, k, lang)
			}
		}
	}
}
