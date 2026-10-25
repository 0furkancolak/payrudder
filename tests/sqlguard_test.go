package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ADR 0020 §5: SQL is never assembled at run time. This fails on fmt.Sprintf or string concatenation that builds a
// statement. The exceptions cannot be plain sqlc: ClickHouse is not supported by sqlc, and DDL cannot take parameters.
var sqlAssembled = map[string]string{
	"services/analytics-svc/internal/usage/query/builder.go": "ClickHouse: sqlc does not support it",
	"pkg/testkit/servicedb.go":                       "DDL for throw-away test databases",
}

var sqlShape = regexp.MustCompile(`(?is)\b(select\b.+\bfrom\b|insert\s+into\b|update\s+\w+\s+set\b|delete\s+from\b)`)

func TestNoAssembledSQL(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	// Files of one directory form one package; an identifier in a format string may be declared in a sibling file.
	dirs := map[string][]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "gen", "sqlc", ".git", ".claude", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			dirs[filepath.Dir(path)] = append(dirs[filepath.Dir(path)], path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, paths := range dirs {
		var files []*ast.File
		for _, path := range paths {
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatal(perr)
			}
			files = append(files, f)
		}
		for _, v := range assembledIn(fset, files) {
			rel, _ := filepath.Rel(root, v.file)
			if sqlAssembled[filepath.ToSlash(rel)] != "" {
				continue
			}
			t.Errorf("%s:%d: SQL assembled at run time (%q...): write a named sqlc query instead", rel, v.line, v.sql[:min(len(v.sql), 40)])
		}
	}
}

// TestAssembledSQLGuardFires proves the guard sees the shapes it exists for, including a format or operand that is an identifier bound to a
// package-level string declared in another file of the package, and that it leaves compile-time constants and unrelated strings alone.
func TestAssembledSQLGuardFires(t *testing.T) {
	sources := map[string]string{
		"a.go": `package p

import "fmt"

const reportSQL = "SELECT %s FROM subscriptions GROUP BY %s"
var deleteSQL = "DELETE FROM t WHERE id = "

const (
	head = "SELECT id "
	full = head + "FROM t" // compile-time constant: fine
)

func literal(c string)     string { return fmt.Sprintf("select %s from t", c) }                       // 1
func viaConst(c string)    string { return fmt.Sprintf(reportSQL, c, c) }                            // 2
func viaVar(id string)     string { return deleteSQL + id }                                          // 3
func viaOther(c string)    string { return fmt.Sprintf(otherSQL, c) }                                // 4
func concatLit(c string)   string { return "select a from t where b = " + c }                        // 5
func concatIdent(c string) string { return c + otherSQL }                                            // 6
func fine(c string)        string { return fmt.Sprintf("%s: %d items", c, 3) } // none
func notSQL(c string)      string { return fmt.Sprintf(msg, c) }                                     // none
`,
		"b.go": `package p

const otherSQL = "UPDATE t SET x = 1 WHERE y = %s"
const msg = "no rows were returned: %s"
`,
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for name, src := range sources {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	var got []string
	for _, v := range assembledIn(fset, files) {
		got = append(got, v.file+":"+strconv.Itoa(v.line))
	}
	sort.Strings(got)
	want := []string{"a.go:13", "a.go:14", "a.go:15", "a.go:16", "a.go:17", "a.go:18"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("flagged %v, want %v", got, want)
	}
}

type assembly struct {
	file string
	line int
	sql  string
}

// assembledIn reports every fmt.Sprintf whose format, and every "a" + b concatenation with an operand, that looks like a statement. A string
// constant or variable declared at package level is resolved by name, so naming the statement does not hide it.
func assembledIn(fset *token.FileSet, files []*ast.File) []assembly {
	decl := map[string]ast.Expr{}
	for _, f := range files {
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || (g.Tok != token.CONST && g.Tok != token.VAR) {
				continue
			}
			for _, sp := range g.Specs {
				vs := sp.(*ast.ValueSpec)
				if len(vs.Values) != len(vs.Names) {
					continue
				}
				for i, n := range vs.Names {
					decl[n.Name] = vs.Values[i]
				}
			}
		}
	}
	var out []assembly
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if s, ok := assembledSQL(n, decl); ok {
				p := fset.Position(n.Pos())
				out = append(out, assembly{file: p.Filename, line: p.Line, sql: s})
			}
			return true
		})
	}
	return out
}

func assembledSQL(n ast.Node, decl map[string]ast.Expr) (string, bool) {
	switch x := n.(type) {
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Sprintf" || len(x.Args) == 0 {
			return "", false
		}
		return sqlString(x.Args[0], decl)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		_, okX := constString(x.X, decl, 0)
		_, okY := constString(x.Y, decl, 0)
		if okX && okY {
			return "", false // joined at compile time, not at run time
		}
		if s, ok := sqlString(x.X, decl); ok {
			return s, true
		}
		return sqlString(x.Y, decl)
	}
	return "", false
}

func sqlString(e ast.Expr, decl map[string]ast.Expr) (string, bool) {
	s, ok := constString(e, decl, 0)
	return s, ok && sqlShape.MatchString(s)
}

// constString evaluates a string literal, an identifier bound to a package-level string, or a + of those.
func constString(e ast.Expr, decl map[string]ast.Expr, depth int) (string, bool) {
	if depth > 8 {
		return "", false
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return constString(x.X, decl, depth+1)
	case *ast.Ident:
		if v, ok := decl[x.Name]; ok {
			return constString(v, decl, depth+1)
		}
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, okL := constString(x.X, decl, depth+1)
		r, okR := constString(x.Y, decl, depth+1)
		return l + r, okL && okR
	}
	return "", false
}
