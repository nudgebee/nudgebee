package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nudgebee/code-analysis-agent/tools/core"
)

// WriteFileTool creates a new file or overwrites an existing file within the
// active workspace. Sister to ReplaceTool: where Replace performs surgical
// edits on an EXISTING file (and refuses to operate until the file has been
// read via file_view), WriteFile is the only tool that can produce a file
// that did not previously exist on disk.
//
// Why this exists: implementation_instructions emitted by specialist agents
// (CodeAgent / ErrorRCA) can legitimately ask the CodeFixer to *create* a new
// workflow file, config, or scaffolded source file. Before this tool existed,
// the fixer's only mutation primitive was Replace — which requires a prior
// file_view and a non-empty old_string — so "create new file" instructions
// failed three times in a row and the circuit-breaker aborted the run with
// max_failures, dropping the upstream RCA work on the floor.
//
// Safety semantics:
//   - The target path MUST resolve inside the workspace. Absolute paths and
//     paths that escape the workspace via "../" are rejected.
//   - By default, writing to an existing file is rejected with a clear error
//     that names the offending path; callers can opt in with overwrite=true.
//   - Parent directories are created on demand (best-effort), so the LLM
//     does not need to chain a separate mkdir CLI call.
//   - On success the tool seeds the FileReadTracker for this path so a
//     subsequent Replace call (e.g. to amend the new file mid-run) does not
//     trip the read-before-edit gate. We treat "I just wrote it" as "I know
//     its contents," which is true by construction.
type WriteFileTool struct {
	workspaceDir string
	readTracker  *FileReadTracker
}

// NewWriteFileTool creates a WriteFileTool bound to a workspace directory.
// workspaceDir is the root that all relative paths resolve against and the
// boundary that absolute / "../" paths must not escape.
func NewWriteFileTool(workspaceDir string) *WriteFileTool {
	return &WriteFileTool{workspaceDir: workspaceDir}
}

// SetReadTracker wires the shared read tracker so successful writes also
// register as reads. Without this the fixer cannot follow up a write with a
// targeted replace on the same file in the same run.
func (t *WriteFileTool) SetReadTracker(tracker *FileReadTracker) {
	t.readTracker = tracker
}

func (t *WriteFileTool) Name() string { return "write_file" }

func (t *WriteFileTool) Description() string {
	return `Create a new file (or overwrite an existing one) with the supplied content. Use this for implementation_instructions with action="write" that introduce a file that does not yet exist on disk — for example, a new GitHub Actions workflow, a new Helm values file, or a freshly scaffolded source file.

REQUIRED: file_path (workspace-relative), content (the complete file body).
OPTIONAL: overwrite (default false — fail if file_path already exists), purpose (one-line semantic description used in logs and error recovery).

DO NOT use write_file to mutate part of an existing file — that is what 'replace' is for. write_file replaces the ENTIRE contents in one shot.

After a successful write you may call 'replace' on the same path without an intermediate file_view; the tool records the post-write contents in the shared read tracker.`
}

func (t *WriteFileTool) GetType() core.NBToolType { return core.NBToolTypeCodeAnalysis }

func (t *WriteFileTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: "object",
		Properties: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Workspace-relative path of the file to create or overwrite. Absolute paths and parent-directory escapes (../) are rejected.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The COMPLETE file body to write. Newlines, indentation, and trailing newline are preserved verbatim.",
			},
			"overwrite": map[string]any{
				"type":        "boolean",
				"description": "If true, replace an existing file at file_path. Default false: writing over an existing file fails with an error that names the path so you can choose explicitly.",
			},
			"purpose": map[string]any{
				"type":        "string",
				"description": "One-line semantic description of what this file accomplishes. Used in logs and self-healing.",
			},
		},
		Required: []string{"file_path", "content"},
	}
}

// Execute writes the requested file. Errors are returned via NBToolResponse
// rather than panics; the caller (ReAct planner) treats Status=="error" as a
// retriable failure with the Error string surfaced back to the LLM.
func (t *WriteFileTool) Execute(_ context.Context, input map[string]any) core.NBToolResponse {
	filePath, _ := input["file_path"].(string)
	content, _ := input["content"].(string)
	overwrite, _ := input["overwrite"].(bool)
	purpose, _ := input["purpose"].(string)

	if strings.TrimSpace(filePath) == "" {
		return core.CreateErrorResponse("file_path is required", "")
	}
	// content may legitimately be empty (e.g. creating a placeholder), but
	// we still require the field to be present (covered by the type check
	// above — a missing key gives content == "").

	// Workspace is required for relative path resolution. We refuse to write
	// to absolute filesystem paths to prevent the LLM from clobbering files
	// outside the cloned repo.
	repoDir := t.workspaceDir
	if workingDir, ok := input["working_directory"].(string); ok && workingDir != "" {
		repoDir = workingDir
	}
	if repoDir == "" {
		return core.CreateErrorResponse("write_file requires a workspace; no workspace_dir is configured", "")
	}

	resolvedAbsPath, err := t.resolveWorkspacePath(repoDir, filePath)
	if err != nil {
		return core.CreateErrorResponse(err.Error(), "")
	}

	// Existence check is intentionally split from the write so the error
	// message can name the path the LLM tried to clobber.
	if _, statErr := os.Stat(resolvedAbsPath); statErr == nil {
		if !overwrite {
			return core.CreateErrorResponse(
				fmt.Sprintf("File '%s' already exists. Set overwrite=true if you intend to replace its full contents, or use 'replace' for a surgical edit.", filePath),
				"")
		}
	} else if !os.IsNotExist(statErr) {
		return core.CreateErrorResponse(fmt.Sprintf("Cannot stat '%s': %v", filePath, statErr), "")
	}

	// Create parent directories. MkdirAll is idempotent — if they exist,
	// this is a no-op; if any leaf is missing, it's created with sane perms.
	if mkErr := os.MkdirAll(filepath.Dir(resolvedAbsPath), 0o755); mkErr != nil {
		return core.CreateErrorResponse(fmt.Sprintf("Failed to create parent directories for '%s': %v", filePath, mkErr), "")
	}

	if writeErr := os.WriteFile(resolvedAbsPath, []byte(content), 0o644); writeErr != nil {
		return core.CreateErrorResponse(fmt.Sprintf("Failed to write '%s': %v", filePath, writeErr), "")
	}

	// Seed the read tracker. Without this, a follow-up 'replace' on the
	// same path would be rejected by the read-before-edit gate.
	if t.readTracker != nil {
		t.readTracker.RecordRead(resolvedAbsPath)
	}

	observation := fmt.Sprintf("Wrote %d bytes to %s", len(content), filePath)
	if purpose != "" {
		observation += " (" + purpose + ")"
	}
	return core.CreateSuccessResponse(observation, observation, map[string]any{
		"file_path":     filePath,
		"absolute_path": resolvedAbsPath,
		"bytes_written": len(content),
		"created":       true,
	})
}

// resolveWorkspacePath joins repoDir and rel, validating that the result
// stays within repoDir. Returns the cleaned absolute path or an error if
// the input would escape the workspace (absolute paths, "../" climbs).
//
// Implementation note: rel.Eval after Join would be cleaner with Go 1.20's
// filepath.IsLocal, but we operate on toolchain-portable basics here.
func (t *WriteFileTool) resolveWorkspacePath(repoDir, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("file_path %q must be workspace-relative, not absolute", rel)
	}
	// Strip an accidental repo-name prefix so callers can pass either
	// "deploy/foo.yaml" or "<repo-name>/deploy/foo.yaml" interchangeably —
	// matching the ergonomics of the replace tool.
	repoName := filepath.Base(repoDir)
	rel = strings.TrimPrefix(rel, repoName+"/")

	joined := filepath.Join(repoDir, rel)
	cleanRepo := filepath.Clean(repoDir)
	cleanJoined := filepath.Clean(joined)

	// Boundary check: after cleaning, the result must still sit under the
	// workspace. Using rel.Eval here would also resolve symlinks, but for
	// our threat model (LLM-driven path construction) a textual prefix
	// guard is enough — we don't allow symlink escapes within the workspace
	// because the workspace is a fresh `git clone` directory.
	if cleanJoined != cleanRepo && !strings.HasPrefix(cleanJoined, cleanRepo+string(os.PathSeparator)) {
		return "", fmt.Errorf("file_path %q escapes workspace %q", rel, repoDir)
	}
	return cleanJoined, nil
}
