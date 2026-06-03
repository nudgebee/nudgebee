package observability

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"nudgebee/services/query"
)

// TestAllSourcesReturnCatalogedOperators is a drift guard. It AST-parses every
// non-test .go file in this package, finds every GetSupportedOperators
// implementation, extracts the string literals it returns, and asserts each
// token has a descriptor in query.OperatorCatalog.
//
// Adding a new provider source or a new token to an existing source without
// updating query.OperatorCatalog will fail this test — CI then blocks the PR
// until the catalog entry is added, preventing the silent-drop bug described
// in issue #29174.
func TestAllSourcesReturnCatalogedOperators(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read observability dir: %v", err)
	}

	fset := token.NewFileSet()
	var tokens []sourceToken

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(".", name)
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "GetSupportedOperators" || fn.Recv == nil {
				return true
			}
			recv := receiverTypeName(fn.Recv)
			for _, stmt := range fn.Body.List {
				ret, ok := stmt.(*ast.ReturnStmt)
				if !ok {
					continue
				}
				for _, r := range ret.Results {
					for _, lit := range stringLiteralsIn(r) {
						tokens = append(tokens, sourceToken{
							file:  name,
							recv:  recv,
							token: lit,
						})
					}
				}
			}
			return true
		})
	}

	if len(tokens) == 0 {
		t.Fatalf("no GetSupportedOperators() string literals discovered — AST scan is broken")
	}

	seen := map[string]bool{}
	for _, s := range tokens {
		if _, ok := query.OperatorCatalog[s.token]; !ok {
			t.Errorf("%s (%s) returns token %q that is not in query.OperatorCatalog — add a descriptor",
				s.recv, s.file, s.token)
		}
		seen[s.token] = true
	}
}

type sourceToken struct {
	file  string
	recv  string
	token string
}

func receiverTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if ident, ok := t.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// stringLiteralsIn walks an expression and collects all BasicLit strings.
// Handles both `return []string{"_eq", "_neq"}` and `return []string{ ...,
// "_gt", "_gte" }` (multi-line composite literals).
func stringLiteralsIn(expr ast.Expr) []string {
	var out []string
	ast.Inspect(expr, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		unq, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		out = append(out, unq)
		return true
	})
	return out
}
