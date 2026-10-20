package errkey

import (
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

var keyRe = regexp.MustCompile(`errkey\.Newf?\(\s*codes\.\w+,\s*"([a-z0-9_]+(?:\.[a-z0-9_]+)+)"`)
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
