package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file larger than the cap must be bounded (clamped + paged), not rejected with an
// error, and the response must tell the agent how to read the next chunk.
func TestFileView_ClampsLargeFileInsteadOfErroring(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 700; i++ { // > maxLinesDefault (500)
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", 3))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileViewTool(dir)
	resp := tool.Execute(context.Background(), map[string]any{
		"file_path":         "big.txt",
		"working_directory": dir,
	})

	if resp.Status == "error" {
		t.Fatalf("expected a bounded read, got error: %s", resp.Error)
	}
	if !strings.Contains(resp.Observation, "TRUNCATED") {
		t.Fatalf("expected a TRUNCATED marker guiding the agent to page; observation:\n%s", resp.Observation)
	}
	// Must include the last in-window line (500) and exclude the first out-of-window line (501).
	if !strings.Contains(resp.Observation, "500: ") {
		t.Errorf("expected line 500 in the bounded window")
	}
	if strings.Contains(resp.Observation, "501: ") {
		t.Errorf("line 501 must not appear (it is beyond the %d-line cap)", maxLinesDefault)
	}
}
