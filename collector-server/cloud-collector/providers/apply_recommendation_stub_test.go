package providers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyRecommendationHasNoSilentSuccessStubs fails when a provider's
// ApplyRecommendation body is just `return nil`. The apply path treats a nil
// error as success and settles the recommendation, so a stub that does nothing
// would mark it applied while the cloud account is unchanged. Services that
// cannot apply a recommendation must return errors.ErrUnsupported instead.
func TestApplyRecommendationHasNoSilentSuccessStubs(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("*", "*.go"))
	if err != nil {
		t.Fatalf("glob provider sources: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "ApplyRecommendation" || fn.Body == nil {
				continue
			}
			checked++
			if len(fn.Body.List) != 1 {
				continue
			}
			ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				continue
			}
			if ident, ok := ret.Results[0].(*ast.Ident); ok && ident.Name == "nil" {
				t.Errorf("%s: ApplyRecommendation returns nil without acting; return errors.ErrUnsupported instead",
					fset.Position(fn.Pos()))
			}
		}
	}

	if checked == 0 {
		t.Fatal("found no ApplyRecommendation implementations; the source glob is wrong")
	}
}
