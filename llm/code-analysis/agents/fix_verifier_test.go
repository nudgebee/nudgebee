package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/tools"
)

// scaffoldRepo creates a git repo with a nested Go module (monorepo shape — the
// manifest is NOT at the repo root, mirroring the layout the fixer historically
// guessed wrong) plus a non-module doc file.
func scaffoldRepo(t *testing.T, mainGo string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	modDir := filepath.Join(dir, "backend", "svc")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(modDir, "go.mod"), "module example.com/svc\n\ngo 1.21\n")
	writeFile(t, filepath.Join(modDir, "main.go"), mainGo)
	writeFile(t, filepath.Join(dir, "README.md"), "docs\n")
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const goodMain = "package main\n\nfunc main() {}\n"

func TestVerifyPassesOnCleanModule(t *testing.T) {
	dir := scaffoldRepo(t, goodMain)
	// A working-tree change inside the module — the fix the verifier must scope to.
	writeFile(t, filepath.Join(dir, "backend", "svc", "main.go"), "package main\n\nvar touched = true\n\nfunc main() { _ = touched }\n")

	v := NewFixVerifier(nil, 2*time.Minute)
	res := v.Verify(context.Background(), dir, nil)
	if res.Status != VerificationVerified {
		t.Fatalf("want verified, got %s (evidence: %s)", res.Status, res.Evidence())
	}
	if len(res.Steps) != 1 || res.Steps[0].Dir != filepath.Join("backend", "svc") {
		t.Fatalf("verification must run from the module dir, got steps %+v", res.Steps)
	}
}

func TestVerifyFailsWithRealCompilerEvidence(t *testing.T) {
	dir := scaffoldRepo(t, goodMain)
	// Introduce the incident's failure class: an unparseable file in the module.
	writeFile(t, filepath.Join(dir, "backend", "svc", "main.go"), "package main\n\n<<<<<<< HEAD\nfunc main() {}\n")

	v := NewFixVerifier(nil, 2*time.Minute)
	res := v.Verify(context.Background(), dir, nil)
	if res.Status != VerificationFailed {
		t.Fatalf("want failed, got %s", res.Status)
	}
	ev := res.Evidence()
	if !strings.Contains(ev, "main.go") {
		t.Errorf("evidence must carry the real compiler output, got: %q", ev)
	}
	if strings.Contains(ev, "failed: \n") || strings.TrimSpace(ev) == "" {
		t.Errorf("evidence must never be empty (the incident's blind-retry cause), got: %q", ev)
	}
}

func TestVerifyUnverifiedWhenNoModuleTouched(t *testing.T) {
	dir := scaffoldRepo(t, goodMain)
	writeFile(t, filepath.Join(dir, "README.md"), "docs changed\n")

	v := NewFixVerifier(nil, time.Minute)
	res := v.Verify(context.Background(), dir, nil)
	// README.md matches no module (no root manifest in the scaffold) → honest unverified.
	if res.Status != VerificationUnverified {
		t.Fatalf("want unverified, got %s (evidence: %s)", res.Status, res.Evidence())
	}
	if res.Reason == "" {
		t.Error("unverified must state why")
	}
}

func TestVerifyBuildConfigOverridesDiscovery(t *testing.T) {
	dir := scaffoldRepo(t, goodMain)
	writeFile(t, filepath.Join(dir, "backend", "svc", "main.go"), goodMain+"\n// touched\n")

	v := NewFixVerifier(nil, time.Minute)
	res := v.Verify(context.Background(), dir, &session.BuildConfig{
		BuildCommand: "sh -c 'echo custom-build-ran'",
	})
	if res.Status != VerificationVerified {
		t.Fatalf("want verified, got %s (evidence: %s)", res.Status, res.Evidence())
	}
	if len(res.Steps) != 1 || !strings.Contains(res.Steps[0].Command, "custom-build-ran") {
		t.Fatalf("BuildConfig must win over discovery, got steps %+v", res.Steps)
	}
}

func TestVerifyBuildConfigFailureStops(t *testing.T) {
	dir := scaffoldRepo(t, goodMain)
	v := NewFixVerifier(nil, time.Minute)
	res := v.Verify(context.Background(), dir, &session.BuildConfig{
		LintCommand:  "sh -c 'echo lint broke; exit 3'",
		BuildCommand: "sh -c 'echo must-not-run'",
	})
	if res.Status != VerificationFailed {
		t.Fatalf("want failed, got %s", res.Status)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("later steps must not run after a failure, got %d steps", len(res.Steps))
	}
	if !strings.Contains(res.Evidence(), "lint broke") {
		t.Errorf("evidence must carry the failing step's output, got: %q", res.Evidence())
	}
}

func TestModulesForFilesPicksDeepestRoot(t *testing.T) {
	roots := []tools.ModuleRoot{
		{Path: ".", Kind: "go", Build: "go build ./..."},
		{Path: "backend/svc", Kind: "go", Build: "go build ./..."},
	}
	mods := modulesForFiles(roots, []string{"backend/svc/main.go", "README.md"})
	if len(mods) != 2 {
		t.Fatalf("want root + nested module, got %+v", mods)
	}
	if mods[1].Path != "backend/svc" {
		t.Errorf("nested file must map to the deepest module, got %+v", mods)
	}
}
