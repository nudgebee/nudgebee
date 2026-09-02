package tools

import (
	"context"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/tools/core"
)

type failingTool struct{ err string }

func (f failingTool) Name() string                 { return "repo_clone" }
func (f failingTool) Description() string          { return "stub" }
func (f failingTool) InputSchema() core.ToolSchema { return core.ToolSchema{Type: "object"} }
func (f failingTool) GetType() core.NBToolType     { return core.NBToolTypeTool }
func (f failingTool) Execute(context.Context, map[string]any) core.NBToolResponse {
	return core.CreateErrorResponse(f.err, "Repository cloning failed")
}

// The tracker only populates TrackedToolInvocation.Error from the error argument
// of CompleteInvocation, and the wrapper used to pass nil. Every wrapper-tracked
// failure therefore carried a status with no cause, which left callers unable to
// report why a step failed — the orchestrator's clone-failure path needs the git
// message to tell a wrong-token 404 apart from a network blip.
func TestTrackedWrapperRecordsToolError(t *testing.T) {
	tracker := common.NewToolInvocationTracker("test-analysis")
	wrapped := NewTrackedToolWrapper(failingTool{err: "remote: Repository not found"}, tracker, nil)

	wrapped.Execute(context.Background(), map[string]any{"repo_url": "https://github.com/org/repo"})

	invocations := tracker.GetInvocations()
	if len(invocations) != 1 {
		t.Fatalf("expected 1 tracked invocation, got %d", len(invocations))
	}
	if invocations[0].Status != "error" {
		t.Errorf("status = %q, want error", invocations[0].Status)
	}
	if !strings.Contains(invocations[0].Error, "Repository not found") {
		t.Errorf("the tool's error should be recorded on the invocation; got %q", invocations[0].Error)
	}
}
