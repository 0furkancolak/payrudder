package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ADR 0020 §5: SQL is never assembled at run time. This fails on fmt.Sprintf or string concatenation that builds a
// statement. The exceptions cannot be plain sqlc: ClickHouse is not supported by sqlc, and DDL cannot take parameters.
var sqlAssembled = map[string]string{
	"services/analytics-svc/internal/query/query.go": "ClickHouse: sqlc does not support it",
	"pkg/testkit/servicedb.go":                       "DDL for throw-away test databases",
}

var sqlShape = regexp.MustCompile(`(?is)\b(select\b.+\bfrom\b|insert\s+into\b|update\s+\w+\s+set\b|delete\s+from\b)`)

func TestNoAssembledSQL(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "gen", "sqlc", ".git", ".claude", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || sqlAssembled[filepath.ToSlash(rel)] != "" {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := assembledSQL(n); ok {
				t.Errorf("%s: SQL assembled at run time (%q...): write a named sqlc query instead", fset.Position(n.Pos()), lit[:min(len(lit), 40)])
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// assembledSQL reports a fmt.Sprintf format or a "a" + b concatenation whose literal part looks like a statement.
func assembledSQL(n ast.Node) (string, bool) {
	switch x := n.(type) {
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Sprintf" || len(x.Args) == 0 {
			return "", false
		}
		return stringLit(x.Args[0])
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		if s, ok := stringLit(x.X); ok {
			return s, true
		}
		return stringLit(x.Y)
	}
	return "", false
}

func stringLit(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(b.Value)
	return s, err == nil && sqlShape.MatchString(s)
}
