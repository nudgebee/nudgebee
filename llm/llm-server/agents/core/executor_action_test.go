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

// runCountingTool counts Calls, so a test can tell "ran" from "refused".
type runCountingTool struct {
	MockContextCapturingTool
	calls int
}

func (c *runCountingTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	c.calls++
	return toolcore.NBToolResponse{Status: toolcore.NBToolResponseStatusSuccess, Data: "ran"}, nil
}

// Native args that aren't a JSON object must not run #39704. Before, the raw text became the
// command & this optional-only tool (empty schema) "succeeded".
func TestDoAction_MalformedNativeArgumentsDoNotRun(t *testing.T) {
	request := NBAgentRequest{AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc", ConversationId: "conv-1", MessageId: "msg-1"}
	run := func(t *testing.T, nativeArgs string) (NBAgentPlannerToolActionStep, int) {
		t.Helper()
		tool := &runCountingTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "logs"}}
		tools := []toolcore.NBTool{tool}
		executor := newPlannerExecutor(security.NewRequestContextForSuperAdmin(), &mockNBAgentPlanner{}, &MockAgent{SupportedTools: tools}, request, 3)
		executor.toolCallbackHandler = nil
		step, finish, err := executor.doAction(getNameToTool(tools), NBAgentPlannerToolAction{
			ToolID: "t1", Tool: "logs", ToolInput: nativeArgs, NativeToolInput: nativeArgs,
		}, "")
		assert.NoError(t, err)
		assert.Nil(t, finish)
		return step, tool.calls
	}

	for name, args := range map[string]string{
		"truncated":         `{"query": "error`,
		"lost value":        `{"query": "error", "_memory_refs": }`,
		"two extra closers": `{"query": "error"}}}`,
		"non-brace suffix":  `{"query": "error"}]`,
		"broken escape":     `{"query": "a\q"}`,
		"unquoted key":      `{query: "error"}`,
		"not an object":     `["error"]`,
		"plain text":        `error in checkout`,
		"null":              `null`,
	} {
		t.Run(name, func(t *testing.T) {
			step, calls := run(t, args)
			assert.Zero(t, calls, "the tool must not run")
			assert.Equal(t, ToolStatusFailure, step.Status)
			assert.Contains(t, step.Observation, "logs did not run")
			assert.Contains(t, step.Observation, "not a valid JSON object")
		})
	}

	for name, args := range map[string]string{
		"valid object": `{"query": "error"}`,
		"no arguments": ``,
	} {
		t.Run(name, func(t *testing.T) {
			step, calls := run(t, args)
			assert.Equal(t, 1, calls)
			assert.Equal(t, ToolStatusSuccess, step.Status)
		})
	}
}

// changingCountingTool is a runCountingTool whose result can change between
// identical calls, like every MCP tool (#39635).
type changingCountingTool struct{ runCountingTool }

func (*changingCountingTool) ResultCanChange() bool { return true }

// A repeated identical call to a tool whose result can change must run again
// and come back as a new step: a browser snapshot after a click must show the
// page after the click (#39635). A tool that does not say keeps the turn cache.
func TestDoAction_ResultCanChangeSkipsTurnCache(t *testing.T) {
	request := NBAgentRequest{AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc", ConversationId: "conv-1", MessageId: "msg-1"}
	run := func(t *testing.T, tool toolcore.NBTool) (first, second NBAgentPlannerToolActionStep, executor *plannerExecutor) {
		t.Helper()
		tools := []toolcore.NBTool{tool}
		executor = newPlannerExecutor(security.NewRequestContextForSuperAdmin(), &mockNBAgentPlanner{}, &MockAgent{SupportedTools: tools}, request, 3)
		executor.toolCallbackHandler = nil
		for i, id := range []string{"snapshot-E1", "snapshot-E3"} {
			step, finish, err := executor.doAction(getNameToTool(tools), NBAgentPlannerToolAction{
				ToolID: id, Tool: "browser_snapshot", ToolInput: `{}`, NativeToolInput: `{}`,
			}, "")
			assert.NoError(t, err)
			assert.Nil(t, finish)
			executor.accumulateSteps([]NBAgentPlannerToolActionStep{step})
			if i == 0 {
				first = step
			} else {
				second = step
			}
		}
		return first, second, executor
	}

	t.Run("result can change", func(t *testing.T) {
		tool := &changingCountingTool{runCountingTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "browser_snapshot"}}}
		_, second, executor := run(t, tool)
		assert.Equal(t, 2, tool.calls, "the repeat must run again, not come from the turn cache")
		assert.False(t, second.IsDuplicateCacheHit)
		assert.Len(t, executor.steps, 2, "the repeat must be kept as a new step")
	})

	t.Run("result does not say", func(t *testing.T) {
		tool := &runCountingTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "browser_snapshot"}}
		_, second, _ := run(t, tool)
		assert.Equal(t, 1, tool.calls, "a tool that does not say keeps the turn cache")
		assert.True(t, second.IsDuplicateCacheHit)
	})
}
