package storage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// A deferred `err = tx.Commit()` only reaches the caller when err is the
// function's named result; otherwise the assignment is lost and a failed commit
// is reported as success. That bug was fixed here function by function several
// times, so this test checks every function in the package instead.
func TestDeferredCommitUsesNamedErrResult(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !deferAssignsCommitToErr(fn.Body) {
				continue
			}
			if !hasNamedErrResult(fn.Type) {
				t.Errorf("%s: %s assigns err = ...Commit() in a defer but err is not a named result, so a failed commit is lost",
					fset.Position(fn.Pos()), fn.Name.Name)
			}
		}
	}
}

func deferAssignsCommitToErr(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		d, ok := n.(*ast.DeferStmt)
		if !ok {
			return !found
		}
		lit, ok := d.Call.Fun.(*ast.FuncLit)
		if !ok {
			return true
		}
		ast.Inspect(lit.Body, func(m ast.Node) bool {
			as, ok := m.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			id, ok := as.Lhs[0].(*ast.Ident)
			call, isCall := as.Rhs[0].(*ast.CallExpr)
			if !ok || !isCall || id.Name != "err" {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Commit" {
				found = true
			}
			return true
		})
		return !found
	})
	return found
}

func hasNamedErrResult(ft *ast.FuncType) bool {
	if ft.Results == nil {
		return false
	}
	for _, f := range ft.Results.List {
		for _, n := range f.Names {
			if n.Name == "err" {
				return true
			}
		}
	}
	return false
}
