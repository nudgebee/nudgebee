package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/common"
)

// End-to-end over the real machinery: a real git repo, a real `git diff -U0`,
// and real pyflakes. Reproduces the astropy-13398 shape — a file that already
// had a pyflakes finding, edited on a different line — and asserts the agent is
// not blamed for what it did not touch.
//
// Skips when pyflakes or git is unavailable rather than failing: this asserts
// behaviour, not the developer's toolchain.
func TestFixVerifier_ChangedLines_RealGitAndPyflakes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if err := exec.Command("python3", "-m", "pyflakes", "--version").Run(); err != nil {
		t.Skip("pyflakes not available")
	}

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	// Committed state carries a pre-existing pyflakes finding on line 2
	// (an unused local), mirroring astropy's own `local variable 'tokens'
	// is assigned to but never used`.
	original := strings.Join([]string{
		"def pre_existing():",
		"    unused_local = 1",
		"    return 2",
		"",
		"",
		"def touched():",
		"    return 'before'",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "mod.py"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "init", "-q", "-b", "main", ".")
	run("git", "add", "-A")
	run("git", "commit", "-qm", "base")

	// The "fix" edits only the last function — nowhere near line 2.
	edited := strings.Replace(original, "return 'before'", "return 'after'", 1)
	if err := os.WriteFile(filepath.Join(dir, "mod.py"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	v := &FixVerifier{logger: common.NewLogger("test", "test", "test", nil)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	lines := v.changedLines(ctx, dir)
	got, ok := lines["mod.py"]
	if !ok {
		t.Fatalf("changedLines found no entry for mod.py; got %v", lines)
	}
	if got[2] {
		t.Error("line 2 was never edited but is marked changed — scoping would not filter the pre-existing finding")
	}
	if !got[7] {
		t.Errorf("the edited line (7) should be marked changed; got %v", got)
	}

	// Real pyflakes output for this file, unscoped, then scoped.
	out, _ := exec.Command("python3", "-m", "pyflakes", filepath.Join(dir, "mod.py")).CombinedOutput()
	raw := strings.ReplaceAll(string(out), dir+"/", "")
	if !strings.Contains(raw, "unused_local") {
		t.Skipf("pyflakes did not report the seeded finding (output: %q)", raw)
	}

	scoped, dropped := scopeLintOutputToChangedLines(raw, lines, "")
	if strings.Contains(scoped, "unused_local") {
		t.Errorf("pre-existing finding survived scoping:\n%s", scoped)
	}
	if dropped == 0 {
		t.Error("expected at least one finding to be dropped")
	}
	if strings.TrimSpace(scoped) != "" {
		t.Errorf("nothing should remain — the edit introduced no findings; got:\n%s", scoped)
	}
}
