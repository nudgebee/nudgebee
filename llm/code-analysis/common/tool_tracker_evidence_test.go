package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func completeRead(t *testing.T, tr *ToolInvocationTracker, tool, path, result, status string) {
	t.Helper()
	id := tr.StartInvocation(tool, map[string]any{"file_path": path})
	tr.CompleteInvocation(id, map[string]any{"status": status, "result": result}, status, nil)
}

// Explore-mode submission leans on this to back an answer whose citations the
// model omitted, so it must report what was actually read — nothing more.
func TestReadFileEvidence(t *testing.T) {
	t.Run("collects successful file_view reads in order, deduplicated", func(t *testing.T) {
		tr := NewToolInvocationTracker("t")
		completeRead(t, tr, "file_view", "a.go", "package a\n\nfunc A() {}", "success")
		completeRead(t, tr, "file_view", "b.go", "package b", "success")
		completeRead(t, tr, "file_view", "a.go", "package a", "success")

		got := tr.ReadFileEvidence()
		require.Len(t, got, 2, "a re-read of the same file is still one piece of evidence")
		assert.Equal(t, "a.go", got[0].Path)
		assert.Equal(t, "b.go", got[1].Path)
		assert.Contains(t, got[0].Snippet, "package a")
	})

	t.Run("ignores failed reads", func(t *testing.T) {
		tr := NewToolInvocationTracker("t")
		completeRead(t, tr, "file_view", "missing.go", "no such file", "error")

		assert.Empty(t, tr.ReadFileEvidence(), "a failed read is not evidence of anything")
	})

	t.Run("ignores tools that reveal no file contents", func(t *testing.T) {
		tr := NewToolInvocationTracker("t")
		completeRead(t, tr, "repo_clone", "", "cloned", "success")
		completeRead(t, tr, "cli", "", "ok", "success")

		assert.Empty(t, tr.ReadFileEvidence())
	})

	// A run can find its answer entirely through search without opening a file;
	// the production failure this targets used rg three times and file_view not
	// at all.
	t.Run("counts ripgrep hits", func(t *testing.T) {
		tr := NewToolInvocationTracker("t")
		id := tr.StartInvocation("rg", map[string]any{"pattern": "Bedrock"})
		tr.CompleteInvocation(id, map[string]any{
			"status": "success",
			"result": "llm/client.go:42:func NewClient() {\nllm/client.go:88:// bedrock\nllm/other.go:7:x",
		}, "success", nil)

		got := tr.ReadFileEvidence()
		require.Len(t, got, 2, "same file matched twice is one piece of evidence")
		assert.Equal(t, "llm/client.go", got[0].Path)
		assert.Equal(t, "func NewClient() {", got[0].Snippet)
		assert.Equal(t, "llm/other.go", got[1].Path)
	})

	t.Run("counts ripgrep files-only output", func(t *testing.T) {
		tr := NewToolInvocationTracker("t")
		id := tr.StartInvocation("rg", map[string]any{"pattern": "x", "files_only": true})
		tr.CompleteInvocation(id, map[string]any{
			"status": "success",
			"result": "llm/client.go\nllm/bedrock_converse.go\n3 files matched in total",
		}, "success", nil)

		got := tr.ReadFileEvidence()
		require.Len(t, got, 2, "the trailing prose line is not a file")
		assert.Equal(t, "llm/client.go", got[0].Path)
	})

	t.Run("a read with no captured output still counts as evidence", func(t *testing.T) {
		tr := NewToolInvocationTracker("t")
		completeRead(t, tr, "file_view", "a.go", "", "success")

		got := tr.ReadFileEvidence()
		require.Len(t, got, 1)
		assert.Equal(t, "a.go", got[0].Path)
	})

	t.Run("snippet is bounded", func(t *testing.T) {
		tr := NewToolInvocationTracker("t")
		completeRead(t, tr, "file_view", "big.go", "l1\nl2\nl3\nl4\nl5\nl6", "success")

		got := tr.ReadFileEvidence()
		require.Len(t, got, 1)
		assert.NotContains(t, got[0].Snippet, "l6", "evidence is an extract, not the file")
	})
}

func TestFirstLines(t *testing.T) {
	assert.Equal(t, "a\nb", firstLines("a\n\n  \nb\nc", 2), "blank lines are skipped, not counted")
	assert.Equal(t, "", firstLines("", 3))
	assert.Equal(t, "only", firstLines("only", 3))
}
