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

// TestVerifyScopedRecoveryVerifiesUntouchedFailure reproduces the incident's shape:
// `go build ./...` fails because an *untouched* package doesn't compile in this
// environment, while the changed package is fine. The verifier must scope the
// rebuild to the changed package and return verified — not block the PR on an
// unrelated failure.
func TestVerifyScopedRecoveryVerifiesUntouchedFailure(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	modDir := filepath.Join(dir, "backend", "svc")
	if err := os.MkdirAll(filepath.Join(modDir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(modDir, "go.mod"), "module example.com/svc\n\ngo 1.21\n")
	writeFile(t, filepath.Join(modDir, "main.go"), goodMain)
	// An untouched sibling package the change never imports, broken in a way that
	// stands in for a cgo-gated dependency that won't build in the sandbox.
	writeFile(t, filepath.Join(modDir, "broken", "broken.go"), "package broken\n\nfunc F() int { return missingSymbol }\n")
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
	// The fix touches only main.go, which does not import ./broken.
	writeFile(t, filepath.Join(modDir, "main.go"), "package main\n\nvar touched = true\n\nfunc main() { _ = touched }\n")

	res := NewFixVerifier(nil, 2*time.Minute).Verify(context.Background(), dir, nil)
	if res.Status != VerificationVerified {
		t.Fatalf("want verified (changed package builds; untouched sibling is broken), got %s (evidence: %s)", res.Status, res.Evidence())
	}
	// Both the failing whole-module build and the passing scoped build are recorded.
	if len(res.Steps) != 2 {
		t.Fatalf("want whole-module + scoped steps, got %d: %+v", len(res.Steps), res.Steps)
	}
	if res.Steps[0].Passed || !res.Steps[1].Passed {
		t.Fatalf("want step0 fail (./...) then step1 pass (scoped), got %+v", res.Steps)
	}
	if !strings.Contains(res.Steps[1].Command, "go build .") || strings.Contains(res.Steps[1].Command, "./...") {
		t.Errorf("scoped step must target the changed package, got %q", res.Steps[1].Command)
	}
}

func TestScopedGoPatterns(t *testing.T) {
	cases := []struct {
		name       string
		changed    []string
		modulePath string
		want       []string
	}{
		{"root module, nested pkg", []string{"ebpftracer/tracer.go", "containers/registry.go"}, ".", []string{"./containers", "./ebpftracer"}},
		{"file at module root", []string{"main.go"}, ".", []string{"."}},
		{"nested module strips prefix", []string{"backend/svc/api/h.go", "backend/svc/main.go"}, "backend/svc", []string{".", "./api"}},
		{"non-go files ignored", []string{"README.md", "go.mod", "pkg/x.go"}, ".", []string{"./pkg"}},
		{"file outside module dropped", []string{"other/x.go", "backend/svc/main.go"}, "backend/svc", []string{"."}},
		{"dedup same package", []string{"pkg/a.go", "pkg/b.go"}, ".", []string{"./pkg"}},
		{"no go files", []string{"README.md"}, ".", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scopedGoPatterns(tc.changed, tc.modulePath)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("scopedGoPatterns(%v, %q) = %v, want %v", tc.changed, tc.modulePath, got, tc.want)
			}
		})
	}
}

func TestScopedFailureImplicatesRepo(t *testing.T) {
	// Real go-nvml / go-systemd output from the incident: every path is under the
	// module cache → an environment failure, not the fix's code.
	depOnly := "github.com/coreos/go-systemd/v22/internal/dlopen: build constraints exclude all Go files in /home/appuser/go/pkg/mod/github.com/coreos/go-systemd/v22@v22.7.0/internal/dlopen\n" +
		"# github.com/NVIDIA/go-nvml/pkg/nvml\n" +
		"/home/appuser/go/pkg/mod/github.com/!n!v!i!d!i!a/go-nvml@v0.13.3-0/pkg/nvml/zz_generated.api.go:817:24: undefined: Return\n"
	if scopedFailureImplicatesRepo(depOnly) {
		t.Error("dependency-only build failure must NOT be attributed to the repo (would wrongly block the PR)")
	}

	inRepo := "# example.com/svc\nebpftracer/tracer.go:166:2: undefined: foo\n"
	if !scopedFailureImplicatesRepo(inRepo) {
		t.Error("in-repo compile error must be attributed to the repo (a real regression)")
	}

	mixed := depOnly + "ebpftracer/tracer.go:170:9: syntax error\n"
	if !scopedFailureImplicatesRepo(mixed) {
		t.Error("any in-repo error among dependency errors must count as a real regression")
	}

	if !scopedFailureImplicatesRepo("linker exploded, no parseable file refs") {
		t.Error("unparseable failure must stay conservative (repo/failed), not relax the gate")
	}
}

func TestWorseStatus(t *testing.T) {
	if worseStatus(VerificationVerified, VerificationUnverified) != VerificationUnverified {
		t.Error("unverified is worse than verified")
	}
	if worseStatus(VerificationFailed, VerificationUnverified) != VerificationFailed {
		t.Error("failed outranks unverified regardless of order")
	}
	if worseStatus(VerificationVerified, VerificationVerified) != VerificationVerified {
		t.Error("verified stays verified")
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
