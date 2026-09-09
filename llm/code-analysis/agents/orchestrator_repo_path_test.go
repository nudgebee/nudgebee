package agents

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression for the SWE-bench finding: repoRelativeFilePath stripped a leading
// "<repoName>/" unconditionally, so a repository containing a top-level directory
// named after itself — astropy/astropy, django/django, sympy/sympy, the ordinary
// Python layout — lost a real path segment. The orchestrator's existence gate then
// reported the file missing and skipped CodeFixer entirely, while submit_analysis
// still returned fixed_code. The caller saw an analysis that described a fix, an
// empty git_diff, and no error.
//
// 414 of 500 SWE-bench Verified instances (83%) have a gold path that begins with
// the repository name, so this fired on the large majority of real repositories.
func TestRepoRelativeFilePath_RepoNamedPackageDirIsNotStripped(t *testing.T) {
	// /tmp/<x>/astropy/astropy/units/quantity.py — the shape that broke.
	root := t.TempDir()
	repoDir := filepath.Join(root, "astropy")
	real := filepath.Join(repoDir, "astropy", "units")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "quantity.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const filePath = "astropy/units/quantity.py"
	got := repoRelativeFilePath(repoDir, filePath)
	if got != filePath {
		t.Fatalf("repoRelativeFilePath(%q, %q) = %q, want it unchanged — the leading segment is a real directory",
			repoDir, filePath, got)
	}

	// The gate this feeds must now find the file; that is the whole point.
	if _, err := os.Stat(filepath.Join(repoDir, got)); err != nil {
		t.Fatalf("existence gate would still skip CodeFixer: %v", err)
	}
}

// The ripgrep-relative case the strip was written for must keep working: there the
// literal path does NOT resolve, so falling through to the strip is correct.
func TestRepoRelativeFilePath_StillStripsWhenPathDoesNotResolve(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "myrepo")
	pkg := filepath.Join(repoDir, "api-server", "services")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// "myrepo/api-server/services/main.go" does not resolve under repoDir
	// (there is no myrepo/myrepo), so the prefix is duplication and must go.
	got := repoRelativeFilePath(repoDir, "myrepo/api-server/services/main.go")
	if want := "api-server/services/main.go"; got != want {
		t.Fatalf("repoRelativeFilePath = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(repoDir, got)); err != nil {
		t.Fatalf("stripped path should resolve: %v", err)
	}
}

// Ambiguity resolves toward the path that exists. Both readings are plausible
// here; only one names a real file.
func TestRepoRelativeFilePath_PrefersTheReadingThatExists(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "sympy")
	// Only sympy/sympy/core/expr.py exists — not sympy/core/expr.py.
	if err := os.MkdirAll(filepath.Join(repoDir, "sympy", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "sympy", "core", "expr.py"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := repoRelativeFilePath(repoDir, "sympy/core/expr.py"); got != "sympy/core/expr.py" {
		t.Fatalf("got %q, want the unstripped path that actually exists", got)
	}
}
