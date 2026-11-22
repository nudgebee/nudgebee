package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// TestKnownNodeTypesIsComplete fails when a NodeType constant in types.go is
// missing from knownNodeTypes, so a custom node kind can never take the name
// of a built-in type.
func TestKnownNodeTypesIsComplete(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "types.go", nil, 0)
	if err != nil {
		t.Fatalf("parse types.go: %v", err)
	}
	found := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 1 {
				continue
			}
			if ident, ok := vs.Type.(*ast.Ident); !ok || ident.Name != "NodeType" {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", lit.Value, err)
			}
			found++
			if !IsKnownNodeType(value) {
				t.Errorf("NodeType %q (%s) is missing from knownNodeTypes", value, vs.Names[0].Name)
			}
		}
	}
	if found != len(knownNodeTypes) {
		t.Errorf("types.go declares %d NodeType constants, knownNodeTypes has %d", found, len(knownNodeTypes))
	}
}
