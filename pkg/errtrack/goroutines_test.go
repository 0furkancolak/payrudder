package errtrack_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A panic in a goroutine nothing guards kills the process without a word to the error tracker (Group.Go and the gRPC and message handlers already report theirs).
// Every `go func() {...}()` in pkg and services therefore starts with `defer errtrack.Recover(ctx)`, or says on the line of the `go` or the line above why not:
// `// no errtrack: <reason>`. A new unguarded goroutine fails here.
func TestEveryGoroutineReportsItsPanic(t *testing.T) {
	root := filepath.Join("..", "..")
	var unguarded []string
	for _, dir := range []string{"pkg", "services"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/gen/") || strings.Contains(path, "/errtrack/") { // the tracker's own goroutines must not report into themselves
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
			if err != nil {
				return err
			}
			excused := map[int]bool{}
			for _, cg := range f.Comments {
				for _, c := range cg.List {
					if strings.Contains(c.Text, "no errtrack:") {
						excused[fset.Position(c.Pos()).Line] = true
					}
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				g, ok := n.(*ast.GoStmt)
				if !ok {
					return true
				}
				line := fset.Position(g.Pos()).Line
				if excused[line] || excused[line-1] || startsWithRecover(g.Call.Fun) {
					return true
				}
				unguarded = append(unguarded, fset.Position(g.Pos()).String())
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range unguarded {
		t.Errorf("%s: goroutine without `defer errtrack.Recover(ctx)` (or a `// no errtrack: <reason>` comment)", u)
	}
}

// startsWithRecover reports whether the function literal defers errtrack.Recover among its first statements.
func startsWithRecover(fn ast.Expr) bool {
	lit, ok := fn.(*ast.FuncLit)
	if !ok {
		return false
	}
	for i, st := range lit.Body.List {
		if i > 3 {
			break
		}
		d, ok := st.(*ast.DeferStmt)
		if !ok {
			continue
		}
		if sel, ok := d.Call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Recover" {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "errtrack" {
				return true
			}
		}
	}
	return false
}
