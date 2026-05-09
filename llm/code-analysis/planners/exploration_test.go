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
