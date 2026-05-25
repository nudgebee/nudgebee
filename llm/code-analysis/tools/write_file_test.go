package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newWriteFileFixture sets up a fresh workspace dir and returns the tool +
// the absolute workspace path. Cleanup is registered via t.Cleanup.
func newWriteFileFixture(t *testing.T) (*WriteFileTool, string) {
	t.Helper()
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	return tool, dir
}

func TestWriteFile_CreatesNewFileWithParentDirs(t *testing.T) {
	tool, ws := newWriteFileFixture(t)
	resp := tool.Execute(context.Background(), map[string]any{
		"file_path": ".github/workflows/proxy-agent-dev-gke.yaml",
		"content":   "name: Proxy-Agent CI\non: [push]\n",
		"purpose":   "Add proxy-agent dev pipeline",
	})
	if resp.Status != "success" {
		t.Fatalf("expected success, got status=%q error=%q", resp.Status, resp.Error)
	}

	abs := filepath.Join(ws, ".github/workflows/proxy-agent-dev-gke.yaml")
	body, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("file not created at %s: %v", abs, err)
	}
	if !strings.Contains(string(body), "name: Proxy-Agent CI") {
		t.Errorf("file body wrong: %q", body)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data wrong type: %T", resp.Data)
	}
	if data["bytes_written"].(int) != len(body) {
		t.Errorf("bytes_written = %v, want %d", data["bytes_written"], len(body))
	}
}

func TestWriteFile_RejectsAbsolutePath(t *testing.T) {
	tool, _ := newWriteFileFixture(t)
	resp := tool.Execute(context.Background(), map[string]any{
		"file_path": "/etc/passwd",
		"content":   "pwned",
	})
	if resp.Status != "error" {
		t.Fatalf("expected error for absolute path, got status=%q", resp.Status)
	}
	if !strings.Contains(resp.Error, "absolute") {
		t.Errorf("error should mention absolute path: %q", resp.Error)
	}
}

func TestWriteFile_RejectsWorkspaceEscape(t *testing.T) {
	tool, _ := newWriteFileFixture(t)
	resp := tool.Execute(context.Background(), map[string]any{
		"file_path": "../escaped.yaml",
		"content":   "should not write",
	})
	if resp.Status != "error" {
		t.Fatalf("expected error for ../ escape, got status=%q", resp.Status)
	}
	if !strings.Contains(resp.Error, "escapes workspace") {
		t.Errorf("error should mention workspace escape: %q", resp.Error)
	}
}

func TestWriteFile_RejectsExistingFileWithoutOverwrite(t *testing.T) {
	tool, ws := newWriteFileFixture(t)
	existing := filepath.Join(ws, "already-here.yaml")
	if err := os.WriteFile(existing, []byte("original"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	resp := tool.Execute(context.Background(), map[string]any{
		"file_path": "already-here.yaml",
		"content":   "new",
	})
	if resp.Status != "error" {
		t.Fatalf("expected error when target exists without overwrite, got status=%q", resp.Status)
	}
	if !strings.Contains(resp.Error, "already exists") {
		t.Errorf("error should mention 'already exists': %q", resp.Error)
	}

	// Existing file content must be untouched.
	body, _ := os.ReadFile(existing)
	if string(body) != "original" {
		t.Errorf("existing file was clobbered: %q", body)
	}
}

func TestWriteFile_OverwritesWithExplicitFlag(t *testing.T) {
	tool, ws := newWriteFileFixture(t)
	existing := filepath.Join(ws, "existing.yaml")
	if err := os.WriteFile(existing, []byte("original"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	resp := tool.Execute(context.Background(), map[string]any{
		"file_path": "existing.yaml",
		"content":   "replacement",
		"overwrite": true,
	})
	if resp.Status != "success" {
		t.Fatalf("expected success with overwrite=true, got status=%q error=%q", resp.Status, resp.Error)
	}

	body, _ := os.ReadFile(existing)
	if string(body) != "replacement" {
		t.Errorf("overwrite did not replace contents: %q", body)
	}
}

func TestWriteFile_StripsRepoNamePrefix(t *testing.T) {
	// repoDir basename is the implicit prefix that some callers include in
	// file_path. The tool should strip it just like ReplaceTool does so the
	// LLM can pass either "deploy/foo.yaml" or "<repo>/deploy/foo.yaml".
	dir := t.TempDir()
	repo := filepath.Join(dir, "nudgebee-infra")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	tool := NewWriteFileTool(repo)

	resp := tool.Execute(context.Background(), map[string]any{
		"file_path": "nudgebee-infra/deploy/foo.yaml",
		"content":   "x",
	})
	if resp.Status != "success" {
		t.Fatalf("expected success, got %q / %q", resp.Status, resp.Error)
	}
	if _, err := os.Stat(filepath.Join(repo, "deploy/foo.yaml")); err != nil {
		t.Errorf("expected file at deploy/foo.yaml, got: %v", err)
	}
}

func TestWriteFile_SeedsReadTracker(t *testing.T) {
	tool, ws := newWriteFileFixture(t)
	tracker := NewFileReadTracker()
	tool.SetReadTracker(tracker)

	resp := tool.Execute(context.Background(), map[string]any{
		"file_path": "new.yaml",
		"content":   "x",
	})
	if resp.Status != "success" {
		t.Fatalf("write failed: %s", resp.Error)
	}

	abs := filepath.Join(ws, "new.yaml")
	if !tracker.WasRead(abs) {
		t.Error("write_file did not seed the read tracker; a follow-up replace would be rejected by the read-before-edit gate")
	}
}

func TestWriteFile_RequiresFilePath(t *testing.T) {
	tool, _ := newWriteFileFixture(t)
	resp := tool.Execute(context.Background(), map[string]any{
		"content": "x",
	})
	if resp.Status != "error" {
		t.Fatalf("expected error for missing file_path, got status=%q", resp.Status)
	}
}

func TestWriteFile_RequiresWorkspace(t *testing.T) {
	tool := NewWriteFileTool("") // unconfigured workspace
	resp := tool.Execute(context.Background(), map[string]any{
		"file_path": "foo.yaml",
		"content":   "x",
	})
	if resp.Status != "error" {
		t.Fatalf("expected error for missing workspace, got status=%q", resp.Status)
	}
}

func TestWriteFile_SchemaShape(t *testing.T) {
	tool, _ := newWriteFileFixture(t)
	schema := tool.InputSchema()
	if schema.Type != "object" {
		t.Errorf("schema.Type = %q, want object", schema.Type)
	}
	for _, k := range []string{"file_path", "content", "overwrite", "purpose"} {
		if _, ok := schema.Properties[k]; !ok {
			t.Errorf("schema.Properties missing %q", k)
		}
	}
	wantRequired := map[string]bool{"file_path": true, "content": true}
	for _, r := range schema.Required {
		if !wantRequired[r] {
			t.Errorf("unexpected required field: %q", r)
		}
		delete(wantRequired, r)
	}
	for missing := range wantRequired {
		t.Errorf("expected required field %q missing from schema", missing)
	}
}
