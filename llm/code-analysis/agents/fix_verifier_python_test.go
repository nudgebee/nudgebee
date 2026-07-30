package agents

import (
	"testing"

	"nudgebee/code-analysis-agent/tools"
)

// The python verify command must be scoped to the module's changed .py files
// (module-relative), and skip when the change touched no Python files —
// whole-module pyflakes would blame pre-existing repo noise on the fix.
func TestPythonScopedLintCommand(t *testing.T) {
	m := tools.ModuleRoot{Path: "notifications-server", Kind: "python", Build: "python3 -m pyflakes"}

	cmd, skip := pythonScopedLintCommand(m, []string{
		"notifications-server/notifications_server/services/message.py",
		"notifications-server/requirements.txt",
		"api-server/other.py", // different module — excluded
	})
	if skip != "" {
		t.Fatalf("unexpected skip: %s", skip)
	}
	want := `python3 -m pyflakes notifications_server/services/message.py`
	if cmd != want {
		t.Errorf("got %q want %q", cmd, want)
	}

	// Root module keeps repo-relative paths.
	root := tools.ModuleRoot{Path: ".", Kind: "python", Build: "python3 -m pyflakes"}
	cmd, skip = pythonScopedLintCommand(root, []string{"a.py", "b/c.py"})
	if skip != "" || cmd != `python3 -m pyflakes a.py b/c.py` {
		t.Errorf("root module scoping wrong: cmd=%q skip=%q", cmd, skip)
	}

	// No changed .py files → skip, not a run.
	if _, skip = pythonScopedLintCommand(m, []string{"notifications-server/requirements.txt"}); skip == "" {
		t.Error("expected skip when no python files changed")
	}
}

// File names come from the ANALYZED repo (attacker-controlled) and the lint
// command runs through a shell — anything shell-active, option-shaped, or
// path-traversing must be dropped, never quoted-and-hoped.
func TestPythonScopedLintCommand_RejectsUnsafePaths(t *testing.T) {
	root := tools.ModuleRoot{Path: ".", Kind: "python", Build: "python3 -m pyflakes"}
	cmd, skip := pythonScopedLintCommand(root, []string{
		"evil$(rm -rf ~).py",
		"space name.py",
		"semi;colon.py",
		"`backtick`.py",
		"-option-injection.py",
		"../escape.py",
		"ok.py",
	})
	if skip != "" {
		t.Fatalf("unexpected skip: %s", skip)
	}
	if cmd != "python3 -m pyflakes ok.py" {
		t.Errorf("unsafe paths must be dropped, got %q", cmd)
	}
	if _, skip = pythonScopedLintCommand(root, []string{"only$(bad).py"}); skip == "" {
		t.Error("all-unsafe change set must skip the module")
	}
}
