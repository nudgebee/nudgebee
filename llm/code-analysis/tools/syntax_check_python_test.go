package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func pyflakesAvailable() bool {
	if _, err := exec.LookPath("pyflakes"); err == nil {
		return true
	}
	if p, err := exec.LookPath("python3"); err == nil {
		if exec.Command(p, "-c", "import pyflakes").Run() == nil {
			return true
		}
	}
	_, err := exec.LookPath("ruff")
	return err == nil
}

// A use-before-definition compiles cleanly (compile() only parses), so the
// undefined-name pass must catch it — this exact class shipped a broken fix
// into a PR.
func TestCheckEditedFileSyntax_PythonUndefinedName(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	if !pyflakesAvailable() {
		t.Skip("no undefined-name checker (pyflakes/ruff) available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.py")
	// group_type used before its definition — valid syntax, undefined name.
	src := "def enqueue(msg):\n    if group_type == 'slo':\n        return 60\n    group_type = msg.get('type')\n    return 3600\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	r := CheckEditedFileSyntax(path)
	if r.Status != SyntaxCheckFailed {
		t.Fatalf("expected failed, got %s (%s): %s", r.Status, r.Checker, r.Detail)
	}
	if !strings.Contains(strings.ToLower(r.Detail), "undefined") && !strings.Contains(strings.ToLower(r.Detail), "f821") {
		t.Errorf("detail must name the undefined-name finding, got: %s", r.Detail)
	}
}

// A clean file must still pass both stages.
func TestCheckEditedFileSyntax_PythonClean(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.py")
	if err := os.WriteFile(path, []byte("def f(x):\n    y = x + 1\n    return y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := CheckEditedFileSyntax(path); r.Status == SyntaxCheckFailed {
		t.Fatalf("clean file must not fail: %s %s", r.Checker, r.Detail)
	}
}
