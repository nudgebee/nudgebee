package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// maxSavedToolOutputBytes bounds the file saveTruncatedToolOutput writes. Only
// the first this-many bytes of a larger output are saved, and the note says so.
const maxSavedToolOutputBytes = 16 << 20

// workspaceOutputCapMarker ends a command output the workspace pod cut at its
// own 1 MiB limit (llm/code-analysis execution handler).
const workspaceOutputCapMarker = "\n...(truncated)"

// toolOutputSaveTimeout bounds the save so a slow or stuck workspace pod costs
// the tool step seconds. It is applied as a context deadline: SaveFile does not
// use the manager's HTTP client (it tries the pod IP with its own short client,
// then the Kubernetes API proxy, which only the context bounds).
const toolOutputSaveTimeout = 15 * time.Second

// saveToolOutputFile writes one file to the conversation's workspace. A
// package-level var so tests can replace it. SaveFile does not create the
// workspace pod; without one it fails and the caller keeps the plain cut.
var saveToolOutputFile = func(ctx *security.RequestContext, accountId, conversationId, filename, content string) error {
	if ctx == nil {
		return fmt.Errorf("saveToolOutputFile: nil request context")
	}
	bounded, cancel := context.WithTimeout(ctx.GetContext(), toolOutputSaveTimeout)
	defer cancel()
	return workspace.NewWorkspaceManager().SaveFile(ctx.WithContext(bounded), accountId, conversationId, filename, content)
}

// saveTruncatedToolOutput saves a tool's full output after truncateToolResponse
// cut it, and returns the note naming the file (shown before the cut output in
// the prompt only; see withSavedOutputNote) plus the file
// reference to attach (nil when nothing was saved). It returns "" when the
// feature is off or the tool already returned its own file (log fetch, metrics,
// traces and event evidence save their own), so nothing is saved twice.
//
// full is the output as the tool returned it; it was already redacted inside
// callNbTool, so the file holds the same text the model was shown in part.
// canReadFiles says whether the agent holds shell_execute: an agent without it
// is told to pass the file name up to a caller that can read it.
func saveTruncatedToolOutput(ctx *security.RequestContext, accountId, conversationId, toolName, full string, refs []toolcore.NBToolResponseReference, canReadFiles bool) (string, *toolcore.NBToolResponseReference) {
	if !config.Config.LlmServerSaveTruncatedToolOutput || hasFileReference(refs) {
		return "", nil
	}
	size := len(full)
	if accountId == "" || conversationId == "" {
		return fmt.Sprintf("[output cut from %d bytes and not saved: no workspace for this conversation; narrow the query to see the part that was cut]", size), nil
	}

	content, saved := unwrapStdoutEnvelope(full), "full output"
	if len(content) > maxSavedToolOutputBytes {
		content = TruncateHead(content, maxSavedToolOutputBytes)
		saved = fmt.Sprintf("first %d bytes", len(content))
	}
	filename := truncatedToolOutputFilename(toolName, content)
	if err := saveToolOutputFile(ctx, accountId, conversationId, filename, content); err != nil {
		if ctx != nil {
			ctx.GetLogger().Warn("plannerexecutor: failed to save truncated tool output", "tool", toolName, "bytes", size, "error", err)
		}
		return fmt.Sprintf("[output cut from %d bytes and could not be saved to the workspace; narrow the query to see the part that was cut]", size), nil
	}
	if ctx != nil {
		ctx.GetLogger().Info("plannerexecutor: truncated tool output saved", "tool", toolName, "bytes", size, "saved_bytes", len(content), "file", filename)
	}

	ref := &toolcore.NBToolResponseReference{
		Text:        filename,
		Url:         filename,
		Type:        "file",
		Description: fmt.Sprintf("Output of %s cut in the response (%s of %d bytes saved)", toolName, saved, size),
	}
	// File name first: callers that keep only the start of an observation
	// (sub-agent evidence, ReAct4's cut) still see it.
	if strings.HasSuffix(content, workspaceOutputCapMarker) {
		// The workspace pod had already capped the command's own output, so
		// the file is incomplete too; only a redirect inside the command keeps
		// all of it.
		return fmt.Sprintf("[workspace file %q holds this output, but the workspace had already cut the command's output at its 1 MiB limit, so the file is incomplete; to keep all of it, re-run the command redirected to a file (command > out.json) and read that file]", filename), ref
	}
	if canReadFiles {
		return fmt.Sprintf("[workspace file %q holds the %s of this cut output (%d bytes); read it with shell_execute (grep, jq, python) instead of re-running this tool]", filename, saved, size), ref
	}
	return fmt.Sprintf("[workspace file %q holds the %s of this cut output (%d bytes); you cannot read files: narrow the query, or name this file in your answer so an agent with shell_execute can read it]", filename, saved, size), ref
}

func hasFileReference(refs []toolcore.NBToolResponseReference) bool {
	for _, r := range refs {
		if r.Type == "file" && r.Url != "" {
			return true
		}
	}
	return false
}

// truncatedToolOutputFilename names the file after the tool and a hash of the
// output, with a .json extension when the output is one JSON document so jq
// works on it directly. The hash (not a timestamp) keeps a repeated identical
// output on one file instead of one file per call.
func truncatedToolOutputFilename(toolName, full string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(toolName) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	name := b.String()
	if name == "" {
		name = "output"
	}
	ext := "txt"
	if json.Valid([]byte(full)) {
		ext = "json"
	}
	sum := sha256.Sum256([]byte(full))
	return fmt.Sprintf("tool_%s_%s.%s", name, hex.EncodeToString(sum[:6]), ext)
}

// FileReferences returns the workspace-file references in refs. Agent-tool
// wrappers that build their own references (metrics, traces) add these from
// the sub-agent's response, so a file a sub-agent step saved still reaches the
// calling agent as a structured reference.
func FileReferences(refs []toolcore.NBToolResponseReference) []toolcore.NBToolResponseReference {
	var out []toolcore.NBToolResponseReference
	for _, r := range refs {
		if r.Type == "file" && r.Url != "" {
			out = append(out, r)
		}
	}
	return out
}

// unwrapStdoutEnvelope returns the command output inside a shell-style result
// ({"stdout": "...", optionally "no_matches"/"exit_code"/an empty "stderr"}), so the saved
// file is what the command printed and `jq`/`grep` work on it directly. Without
// this, a 1 MB `kubectl get pods -A -o json` was saved as one escaped JSON string:
// jq found nothing, and `head` on the one-line file was itself cut and saved
// again. Any other output is returned unchanged.
func unwrapStdoutEnvelope(full string) string {
	if len(full) > 2*maxSavedToolOutputBytes || !strings.HasPrefix(strings.TrimSpace(full), "{") || !strings.Contains(full, `"stdout"`) {
		return full
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(full), &envelope); err != nil {
		return full
	}
	for key := range envelope {
		switch key {
		case "stdout", "stderr", "no_matches", "exit_code":
		default:
			return full
		}
	}
	// stderr would be lost by keeping stdout alone; save the envelope as is.
	if raw, ok := envelope["stderr"]; ok {
		var stderr string
		if err := json.Unmarshal(raw, &stderr); err != nil || stderr != "" {
			return full
		}
	}
	var stdout string
	if err := json.Unmarshal(envelope["stdout"], &stdout); err != nil {
		return full
	}
	return stdout
}

// restoreStepMetadata rebuilds a step's tool metadata from executor state saved
// by Marshal, so a resumed run keeps the saved-output note and footer. Returns
// nil when the state has none or it cannot be read.
func restoreStepMetadata(raw any) *toolcore.NBToolResponseMetadata {
	if raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var metadata toolcore.NBToolResponseMetadata
	if err := json.Unmarshal(b, &metadata); err != nil {
		return nil
	}
	return &metadata
}

// withSavedOutputNote puts a step's saved-output note before its observation
// when the prompt is built. Before, not after: sub-agent evidence and ReAct4's
// cut keep only the start and end of a long observation.
func withSavedOutputNote(observation string, metadata *toolcore.NBToolResponseMetadata) string {
	if metadata == nil || metadata.SavedOutputNote == "" {
		return observation
	}
	return metadata.SavedOutputNote + "\n" + observation
}
