package planners

import (
	"context"
	"testing"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/tools"
	"nudgebee/code-analysis-agent/tools/core"
)

type dedupStubTool struct{}

func (dedupStubTool) Name() string                 { return "file_view" }
func (dedupStubTool) Description() string          { return "stub" }
func (dedupStubTool) InputSchema() core.ToolSchema { return core.ToolSchema{} }
func (dedupStubTool) GetType() core.NBToolType     { return core.NBToolTypeTool }
func (dedupStubTool) Execute(_ context.Context, _ map[string]any) core.NBToolResponse {
	return core.CreateSuccessResponse("ok", "ok", nil)
}

// TestToolSelfTracks is the guard for the dual-write fix: a tool wrapped with the
// planner's own tracker self-tracks (so the planner must not track it again),
// while a bare tool — or a wrapper bound to a different tracker — does not.
func TestToolSelfTracks(t *testing.T) {
	tracker := common.NewToolInvocationTracker("a")
	p := &ReActPlanner{}
	p.SetToolTracker(tracker)

	bare := dedupStubTool{}
	wrappedSame := tools.NewTrackedToolWrapper(bare, tracker, nil)
	wrappedOther := tools.NewTrackedToolWrapper(bare, common.NewToolInvocationTracker("b"), nil)

	if !p.toolSelfTracks(wrappedSame) {
		t.Error("wrapper bound to the planner's tracker should self-track")
	}
	if p.toolSelfTracks(bare) {
		t.Error("bare tool should not be considered self-tracking")
	}
	if p.toolSelfTracks(wrappedOther) {
		t.Error("wrapper bound to a different tracker should not be considered self-tracking")
	}

	// With no tracker set, nothing self-tracks (planner has nothing to dedupe against).
	if (&ReActPlanner{}).toolSelfTracks(wrappedSame) {
		t.Error("with nil tracker, toolSelfTracks must be false")
	}
}
