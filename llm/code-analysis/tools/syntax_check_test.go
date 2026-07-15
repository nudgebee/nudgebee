package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSyntaxCheck_GoValidAndBroken(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not available")
	}
	ok := CheckEditedFileSyntax(writeTemp(t, "ok.go", "package x\n\nfunc F() int { return 1 }\n"))
	if ok.Status != SyntaxCheckPassed {
		t.Fatalf("valid go must pass, got %s (%s)", ok.Status, ok.Detail)
	}
	bad := CheckEditedFileSyntax(writeTemp(t, "bad.go", "package x\n\nfunc F() int { return 1\n")) // missing brace
	if bad.Status != SyntaxCheckFailed || bad.Detail == "" {
		t.Fatalf("broken go must fail with detail, got %s (%q)", bad.Status, bad.Detail)
	}
}

func TestSyntaxCheck_PythonValidAndBroken(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	ok := CheckEditedFileSyntax(writeTemp(t, "ok.py", "def f():\n    return 1\n"))
	if ok.Status != SyntaxCheckPassed {
		t.Fatalf("valid python must pass, got %s (%s)", ok.Status, ok.Detail)
	}
	bad := CheckEditedFileSyntax(writeTemp(t, "bad.py", "def f(:\n    return 1\n"))
	if bad.Status != SyntaxCheckFailed || bad.Detail == "" {
		t.Fatalf("broken python must fail with detail, got %s (%q)", bad.Status, bad.Detail)
	}
}

func TestSyntaxCheck_TriStateGuarantees(t *testing.T) {
	// No parser for the language -> silently not checked (never a failure).
	r := CheckEditedFileSyntax(writeTemp(t, "x.ts", "const a: = ;"))
	if r.Status != SyntaxCheckNotChecked {
		t.Fatalf("unknown extension must be not_checked, got %s", r.Status)
	}
	// Kill switch.
	t.Setenv("POST_EDIT_SYNTAX_CHECK", "0")
	r = CheckEditedFileSyntax(writeTemp(t, "y.go", "package x\nfunc broken( {\n"))
	if r.Status != SyntaxCheckNotChecked {
		t.Fatalf("disabled gate must be not_checked, got %s", r.Status)
	}
}

func TestSyntaxCheck_AppendToObservation(t *testing.T) {
	base := "Successfully replaced in foo.go"
	failed := SyntaxCheckResult{Status: SyntaxCheckFailed, Checker: "gofmt -e", Detail: "foo.go:3:1: expected '}'"}
	got := failed.AppendToObservation(base)
	if !strings.Contains(got, "SYNTAX CHECK FAILED") || !strings.Contains(got, "expected '}'") || !strings.Contains(got, base) {
		t.Fatalf("failure note malformed: %q", got)
	}
	passed := SyntaxCheckResult{Status: SyntaxCheckPassed, Checker: "gofmt -e"}
	if got := passed.AppendToObservation(base); !strings.Contains(got, "[syntax OK: gofmt -e]") {
		t.Fatalf("pass note missing: %q", got)
	}
	nc := SyntaxCheckResult{Status: SyntaxCheckNotChecked}
	if got := nc.AppendToObservation(base); got != base {
		t.Fatalf("not_checked must append nothing: %q", got)
	}
}
