package core

import (
	"testing"

	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestWriteConfirmationRequired: write tools (create/update/delete) prompt, except
// watch_resource which is exempt; watch_cancel is intentionally NOT exempt.
func TestWriteConfirmationRequired(t *testing.T) {
	rt := func(t toolcore.ToolRequestType) *toolcore.ToolRequestType { return &t }

	cases := []struct {
		name        string
		requestType *toolcore.ToolRequestType
		toolName    string
		want        bool
	}{
		{"nil request type", nil, "kubectl_execute", false},
		{"empty request type", rt(""), "kubectl_execute", false},
		{"read is never confirmed", rt(toolcore.ToolRequestTypeRead), "kubectl_execute", false},
		{"create write tool confirms", rt(toolcore.ToolRequestTypeCreate), "kubectl_execute", true},
		{"update write tool confirms", rt(toolcore.ToolRequestTypeUpdate), "aws_execute", true},
		{"delete write tool confirms", rt(toolcore.ToolRequestTypeDelete), "shell_execute", true},
		{"watch_resource create is exempt", rt(toolcore.ToolRequestTypeCreate), tools.ToolWatchResource, false},
		{"watch_resource exempt case-insensitive", rt(toolcore.ToolRequestTypeCreate), "Watch_Resource", false},
		{"watch_cancel update still confirms", rt(toolcore.ToolRequestTypeUpdate), tools.ToolWatchCancel, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, writeConfirmationRequired(tc.requestType, tc.toolName))
		})
	}
}

// TestWriteConfirmationRequired_RecommendationWriteTools: the typed resolution
// write tools must never slip into the watch_resource exemption.
func TestWriteConfirmationRequired_RecommendationWriteTools(t *testing.T) {
	rt := func(t toolcore.ToolRequestType) *toolcore.ToolRequestType { return &t }
	assert.True(t, writeConfirmationRequired(rt(toolcore.ToolRequestTypeUpdate), tools.ToolRecommendationApply))
	assert.True(t, writeConfirmationRequired(rt(toolcore.ToolRequestTypeUpdate), tools.ToolRecommendationExecuteCli))
	assert.True(t, writeConfirmationRequired(rt(toolcore.ToolRequestTypeCreate), tools.ToolRecommendationRecordTicketResolution))
}

// confirmStubTool / confirmScopedStubTool exercise confirmationKeyForAction's two paths.
type confirmStubTool struct{ name string }

func (s confirmStubTool) Name() string                     { return s.name }
func (s confirmStubTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (s confirmStubTool) Description() string              { return "" }
func (s confirmStubTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (s confirmStubTool) Call(toolcore.NbToolContext, toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

type confirmScopedStubTool struct{ confirmStubTool }

func (s confirmScopedStubTool) ConfirmationKey(toolInput string) string {
	return s.name + ":" + toolInput
}

func TestConfirmationKeyForAction(t *testing.T) {
	plain := confirmStubTool{name: "plain_tool"}
	scoped := confirmScopedStubTool{confirmStubTool{name: "scoped_tool"}}

	// Unscoped tools keep the historical per-tool key.
	key := confirmationKeyForAction(plain, NBAgentPlannerToolAction{Tool: "plain_tool", ToolInput: "a"})
	assert.Equal(t, "plain_tool", key)

	// Scoped tools key per action: same input → same key, different input → different key.
	k1 := confirmationKeyForAction(scoped, NBAgentPlannerToolAction{Tool: "scoped_tool", ToolInput: "a"})
	k2 := confirmationKeyForAction(scoped, NBAgentPlannerToolAction{Tool: "scoped_tool", ToolInput: "b"})
	assert.Equal(t, "scoped_tool:a", k1)
	assert.NotEqual(t, k1, k2)
}

func TestConfirmationApprovedForAction(t *testing.T) {
	scoped := confirmScopedStubTool{confirmStubTool{name: "scoped_tool"}}
	nameToTool := map[string]toolcore.NBTool{"SCOPED_TOOL": scoped}
	action := NBAgentPlannerToolAction{Tool: "scoped_tool", ToolInput: "a"}

	// Approval recorded under the per-action key is found; an approval under a
	// different action's key is not.
	assert.True(t, confirmationApprovedForAction(map[string]string{"scoped_tool:a": "yes"}, nameToTool, action))
	assert.False(t, confirmationApprovedForAction(map[string]string{"scoped_tool:b": "yes"}, nameToTool, action))
	// A bare per-tool approval no longer unlocks a scoped tool's other actions.
	assert.False(t, confirmationApprovedForAction(map[string]string{"scoped_tool": "yes"}, nameToTool, action))
	// Rejection under the right key stays a rejection.
	assert.False(t, confirmationApprovedForAction(map[string]string{"scoped_tool:a": "no"}, nameToTool, action))

	// Tools absent from the map fall back to the per-tool key.
	unknown := NBAgentPlannerToolAction{Tool: "other_tool", ToolInput: "x"}
	assert.True(t, confirmationApprovedForAction(map[string]string{"other_tool": "yes"}, nameToTool, unknown))
}
