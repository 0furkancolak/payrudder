package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// renameSpec says what the product is called afterwards. The internal identifiers (module path, PAYRUDDER_ variables, container and chart names, file names)
// follow the slug; what people read follows Display.
type renameSpec struct {
	Display string // "Acme Billing"
	Slug    string // "acmebilling": lower-case letters and digits, starting with a letter
	Module  string // "github.com/acme/acmebilling", empty to keep the module owner
}

const (
	oldDisplay = "Payrudder"
	oldSlug    = "payrudder"
	oldModule  = "github.com/0furkancolak/payrudder"
)

// A slug is one lower-case word: it is also used inside Go identifiers, SQL names and image names, where a dash is not allowed.
var slugRe = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

func (s renameSpec) validate() error {
	switch {
	case !slugRe.MatchString(s.Slug):
		return fmt.Errorf("slug %q: one word of lower-case letters and digits, starting with a letter", s.Slug)
	case strings.ContainsAny(s.Display, "\"\\\n"):
		return errors.New("display name must not contain quotes, backslashes or line breaks")
	case strings.TrimSpace(s.Display) == "":
		return errors.New("display name is empty")
	}
	return nil
}

var (
	pascalTokenRe = regexp.MustCompile(`[A-Za-z0-9_]*Payrudder[A-Za-z0-9_]*`)
	upperRe       = regexp.MustCompile(`PAYRUDDER`)
)

// rewrite applies the rename to one file's text. "Payrudder" on its own is the product name people read (the display name); inside a longer word
// (PayrudderOwned, requirePayrudderOwned) it is part of an identifier and becomes the slug in Pascal case.
func (s renameSpec) rewrite(text string) string {
	if s.Module != "" {
		text = strings.ReplaceAll(text, oldModule, s.Module)
	}
	pascal := strings.ToUpper(s.Slug[:1]) + s.Slug[1:]
	text = pascalTokenRe.ReplaceAllStringFunc(text, func(tok string) string {
		if tok == oldDisplay {
			return s.Display
		}
		return strings.ReplaceAll(tok, oldDisplay, pascal)
	})
	text = upperRe.ReplaceAllString(text, strings.ToUpper(s.Slug))
	return strings.ReplaceAll(text, oldSlug, s.Slug)
}

// keep lists files whose text must not change: the licence and notice name the licensor, and go.sum holds hashes of other people's modules.
func keep(path string) bool {
	switch filepath.Base(path) {
	case "LICENSE", "NOTICE", "go.sum":
		return true
	}
	return false
}

type renamePlan struct {
	Edits   map[string]string // path -> new text
	Renames [][2]string       // old path -> new path
}

// planRename reads the files and works out the changes; it writes nothing.
func planRename(root string, files []string, spec renameSpec) (renamePlan, error) {
	plan := renamePlan{Edits: map[string]string{}}
	for _, f := range files {
		if keep(f) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, f)) //nolint:gosec // the repository's own tracked files
		if err != nil {
			return plan, err
		}
		if bytes.IndexByte(b, 0) >= 0 { // binary
			continue
		}
		if out := spec.rewrite(string(b)); out != string(b) {
			plan.Edits[f] = out
		}
		if np := spec.rewrite(f); np != f {
			plan.Renames = append(plan.Renames, [2]string{f, np})
		}
	}
	return plan, nil
}

func (p renamePlan) apply(root string, git bool) error {
	for f, text := range p.Edits {
		if err := os.WriteFile(filepath.Join(root, f), []byte(text), 0o644); err != nil { //nolint:gosec // source files
			return err
		}
	}
	for _, r := range p.Renames {
		dst := filepath.Join(root, r[1])
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // source tree
			return err
		}
		if git {
			if out, err := exec.Command("git", "-C", root, "mv", r[0], r[1]).CombinedOutput(); err != nil { //nolint:gosec // paths of tracked files
				return fmt.Errorf("git mv %s: %w: %s", r[0], err, out)
			}
			continue
		}
		if err := os.Rename(filepath.Join(root, r[0]), dst); err != nil {
			return err
		}
	}
	for _, r := range p.Renames {
		pruneEmpty(root, filepath.Dir(r[0]))
	}
	return nil
}

// pruneEmpty removes dir and its parents up to root while they are empty: a move leaves the old directories behind.
func pruneEmpty(root, dir string) {
	for dir != "." && dir != "/" && dir != "" {
		if err := os.Remove(filepath.Join(root, dir)); err != nil { // fails when it is not empty
			return
		}
		dir = filepath.Dir(dir)
	}
}

// rename implements `payrudderctl rename`.
func rename(args []string) error {
	fs := flag.NewFlagSet("rename", flag.ContinueOnError)
	spec := renameSpec{}
	fs.StringVar(&spec.Display, "name", "", "the new product name as people read it, e.g. \"Acme Billing\"")
	fs.StringVar(&spec.Slug, "slug", "", "the new identifier, one lower-case word, e.g. acmebilling (default: the name in lower case without spaces)")
	fs.StringVar(&spec.Module, "module", "", "the new Go module path of the repository, e.g. github.com/acme/acmebilling (default: keep the owner, change the name)")
	root := fs.String("root", ".", "the repository root")
	apply := fs.Bool("apply", false, "write the changes (default: only list them)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if spec.Slug == "" {
		spec.Slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(spec.Display), "")
	}
	if spec.Module == "" && spec.Slug != oldSlug {
		spec.Module = strings.TrimSuffix(oldModule, "/"+oldSlug) + "/" + spec.Slug
	}
	if err := spec.validate(); err != nil {
		return err
	}
	out, err := exec.Command("git", "-C", *root, "ls-files").Output() //nolint:gosec // a fixed command on the operator's own repository
	if err != nil {
		return fmt.Errorf("git ls-files: %w", err)
	}
	plan, err := planRename(*root, strings.Fields(string(out)), spec)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%d files change, %d files and directories move (name %q, slug %q, module %s)\n", len(plan.Edits), len(plan.Renames), spec.Display, spec.Slug, spec.Module)
	if !*apply {
		fmt.Fprintln(os.Stdout, "nothing written; add --apply, then run `make gen` and `make lint`")
		return nil
	}
	if err := plan.apply(*root, true); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "done: run `make gen`, `go work sync`, `make lint` and the test suites; renamed protos and images need a rebuild")
	return nil
}
