package planners

import (
	"testing"
)

func TestHashToolCall_Deterministic(t *testing.T) {
	p := &ReActPlanner{executedCallHashes: make(map[string]int)}

	input := map[string]any{"pattern": "func main", "type": "go"}
	hash1 := p.hashToolCall("rg", input)
	hash2 := p.hashToolCall("rg", input)

	if hash1 != hash2 {
		t.Errorf("expected deterministic hash, got %q and %q", hash1, hash2)
	}
	if len(hash1) != 16 {
		t.Errorf("expected 16 char hash, got %d chars: %q", len(hash1), hash1)
	}
}

func TestHashToolCall_DifferentInputs(t *testing.T) {
	p := &ReActPlanner{executedCallHashes: make(map[string]int)}

	hash1 := p.hashToolCall("rg", map[string]any{"pattern": "func main"})
	hash2 := p.hashToolCall("rg", map[string]any{"pattern": "class User"})
	hash3 := p.hashToolCall("file_view", map[string]any{"pattern": "func main"})

	if hash1 == hash2 {
		t.Error("expected different hashes for different patterns")
	}
	if hash1 == hash3 {
		t.Error("expected different hashes for different actions")
	}
}

func TestHashToolCall_IgnoresWorkingDirectory(t *testing.T) {
	p := &ReActPlanner{executedCallHashes: make(map[string]int)}

	hash1 := p.hashToolCall("rg", map[string]any{"pattern": "test", "working_directory": "/tmp/a"})
	hash2 := p.hashToolCall("rg", map[string]any{"pattern": "test", "working_directory": "/tmp/b"})

	if hash1 != hash2 {
		t.Error("expected same hash when only working_directory differs")
	}
}

// A run that repeatedly checks git state instead of acting must trip the same
// exploration limiter as ls/find — previously it didn't, because
// isExplorationCommand only recognized action=="cli". Observed live: a
// commit-enforcement retry burned its entire budget on git status/branch/log/
// diff/remote/fetch and never once attempted add/commit/push (issue #36634).
func TestIsExplorationCommand_GitReadOnlySubcommands(t *testing.T) {
	p := &ReActPlanner{}

	readOnly := [][]any{
		{"status"}, {"log", "--oneline"}, {"diff", "README.md"},
		{"show", "abc123"}, {"branch", "-a"}, {"remote", "-v"},
		{"fetch", "origin"}, {"blame", "file.go"},
	}
	for _, args := range readOnly {
		if !p.isExplorationCommand("git", map[string]any{"args": args}) {
			t.Errorf("git %v not classified as exploration", args)
		}
	}

	writes := [][]any{
		{"add", "-A"}, {"commit", "-m", "msg"}, {"push"}, {"checkout", "--", "."},
	}
	for _, args := range writes {
		if p.isExplorationCommand("git", map[string]any{"args": args}) {
			t.Errorf("git %v incorrectly classified as exploration — this would let the limiter block a real commit/push/discard attempt", args)
		}
	}
}

// The pre-existing ls/find classification (via the cli tool) must be unchanged.
func TestIsExplorationCommand_CliUnaffectedByGitChange(t *testing.T) {
	p := &ReActPlanner{}

	if !p.isExplorationCommand("cli", map[string]any{"command": "ls -la"}) {
		t.Error("ls -la via cli no longer classified as exploration")
	}
	if p.isExplorationCommand("cli", map[string]any{"command": "grep -r foo ."}) {
		t.Error("grep via cli incorrectly classified as exploration")
	}
	if p.isExplorationCommand("git", map[string]any{"args": []any{}}) {
		t.Error("git with empty args incorrectly classified as exploration")
	}
}
