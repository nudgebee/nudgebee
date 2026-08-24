package core

import (
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// fakeConversationDao embeds IConversationDao so it satisfies the interface
// while overriding only the method the tool-not-found path calls. Any other
// method would nil-panic — intentional: the test must not depend on more.
type fakeConversationDao struct {
	IConversationDao
}

func (fakeConversationDao) SaveCompletedConversationAgentCall(
	_ uuid.UUID,
	_, _, _, _, _, _, _, _, _, _ string,
	_ toolcore.NBQueryConfig,
	_ AgentExecutionStatus,
	_ string,
) (uuid.UUID, error) {
	return uuid.Nil, nil
}

// TestDoActionToolNotFound verifies doAction's tool-not-found path: an action
// naming a tool absent from nameToTool (and not a client tool) returns a step
// whose observation reports "Tool not found", with no finish action and no
// error. Made hermetic by injecting a no-op ConversationDao (the not-found path
// records a skipped-agent-call row) — the actions carry no dependencies, so
// rewriteToolInput short-circuits without an LLM call. (Was an env-gated,
// skipped e2e test.)
func TestDoActionToolNotFound(t *testing.T) {
	const toolNotFoundMessage = "Tool not found"

	prevDao := conversationDao
	SetConversationDao(fakeConversationDao{})
	t.Cleanup(func() { conversationDao = prevDao })

	executor := &plannerExecutor{
		ctx:   security.NewRequestContextForSuperAdmin(),
		agent: LLMAgent{},
		agentRequest: NBAgentRequest{
			AccountId:      "acct-1",
			UserId:         "user-1",
			ConversationId: "conv-1",
			MessageId:      "msg-1",
			AgentId:        "tickets",
		},
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	testCases := []struct {
		name   string
		action NBAgentPlannerToolAction
	}{
		{
			name: "unknown tool name",
			action: NBAgentPlannerToolAction{
				ToolID:    "test-tool-id-1",
				Tool:      "NONEXISTENT_TOOL",
				ToolInput: "test input",
				Log:       "test log",
			},
		},
		{
			name: "another unknown tool name with JSON input",
			action: NBAgentPlannerToolAction{
				ToolID:    "made-up-2",
				Tool:      "definitely_not_a_real_tool_xyz",
				ToolInput: `{"command":"search"}`,
				Log:       "Action: definitely_not_a_real_tool_xyz",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			step, finish, err := executor.doAction(map[string]toolcore.NBTool{}, tc.action, "")

			assert.NoError(t, err)
			assert.Nil(t, finish)
			assert.Equal(t, tc.action.ToolID, step.Action.ToolID)
			assert.Equal(t, tc.action.Tool, step.Action.Tool)
			assert.Equal(t, tc.action.ToolInput, step.Action.ToolInput)
			assert.Equal(t, tc.action.Log, step.Action.Log)
			assert.Contains(t, step.Observation, toolNotFoundMessage)
		})
	}
}
