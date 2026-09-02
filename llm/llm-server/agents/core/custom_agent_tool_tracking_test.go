package core

import (
	"errors"
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedToolCall captures one SaveConversationToolCall invocation.
type recordedToolCall struct {
	conversationId string
	agentId        string
	toolId         string
	toolName       string
	args           string
	result         string
	status         toolcore.NBToolResponseStatus
	metadata       []byte
}

// trackingFakeDao records SaveConversationToolCall calls in order.
type trackingFakeDao struct {
	IConversationDao
	calls []recordedToolCall
}

func (d *trackingFakeDao) SaveConversationToolCall(
	conversationID, _, _, _, agenId, toolId, toolName, toolArgs, _, _, toolResult string,
	status toolcore.NBToolResponseStatus,
	_ toolcore.NBToolType,
	_ *string,
	_ []toolcore.NBToolResponseReference,
	metadata []byte,
	_ []byte,
) error {
	d.calls = append(d.calls, recordedToolCall{
		conversationId: conversationID, agentId: agenId, toolId: toolId,
		toolName: toolName, args: toolArgs, result: toolResult,
		status: status, metadata: metadata,
	})
	return nil
}

// trackingStubTool is a minimal NBTool whose Call returns a canned result.
type trackingStubTool struct {
	toolcore.NBTool
	resp toolcore.NBToolResponse
	err  error
}

func (trackingStubTool) Name() string                 { return "resource_search_execute" }
func (trackingStubTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (s trackingStubTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return s.resp, s.err
}

func installTrackingDao(t *testing.T) *trackingFakeDao {
	t.Helper()
	dao := &trackingFakeDao{}
	prev := conversationDao
	SetConversationDao(dao)
	t.Cleanup(func() { conversationDao = prev })
	return dao
}

func trackingToolCtx() toolcore.NbToolContext {
	return toolcore.NbToolContext{
		Ctx:            security.NewRequestContextForSuperAdmin(),
		AccountId:      "acct",
		UserId:         "user",
		ConversationId: "conv",
		MessageId:      "msg",
		ParentAgentId:  "agent",
	}
}

// TestCallToolPersistsSuccess pins the contract custom-planner agents lost when
// they moved off the planner loop: a direct tool call must still produce an
// in-progress row followed by a terminal row carrying the wall-clock duration,
// both keyed to the calling agent.
func TestCallToolPersistsSuccess(t *testing.T) {
	dao := installTrackingDao(t)

	resp, err := CallTool(trackingToolCtx(),
		trackingStubTool{resp: toolcore.NBToolResponse{Data: "found"}},
		toolcore.NBToolCallRequest{Command: `{"resource_name":"relay-server"}`})

	require.NoError(t, err)
	assert.Equal(t, "found", resp.Data)
	require.Len(t, dao.calls, 2)

	assert.Equal(t, toolcore.NBToolResponseStatusInProgress, dao.calls[0].status)
	assert.Equal(t, `{"resource_name":"relay-server"}`, dao.calls[0].args)

	assert.Equal(t, toolcore.NBToolResponseStatusSuccess, dao.calls[1].status)
	assert.Equal(t, "found", dao.calls[1].result)
	assert.Equal(t, "conv", dao.calls[1].conversationId)
	assert.Equal(t, "agent", dao.calls[1].agentId)
	assert.Contains(t, string(dao.calls[1].metadata), "execution_duration_ms")
	assert.Contains(t, string(dao.calls[1].metadata), `"exit_status":0`)

	// Both rows must share one tool_id or the upsert splits the lifecycle in two.
	assert.Equal(t, dao.calls[0].toolId, dao.calls[1].toolId)
	assert.NotEmpty(t, dao.calls[0].toolId)
}

// TestCallToolPersistsFailure verifies a failing tool is recorded as an error
// row with the error text as the response, and that the caller still receives
// the original error.
func TestCallToolPersistsFailure(t *testing.T) {
	dao := installTrackingDao(t)

	callErr := errors.New("relay timeout")
	_, err := CallTool(trackingToolCtx(),
		trackingStubTool{err: callErr},
		toolcore.NBToolCallRequest{Command: "{}"})

	assert.ErrorIs(t, err, callErr)
	require.Len(t, dao.calls, 2)
	assert.Equal(t, toolcore.NBToolResponseStatusError, dao.calls[1].status)
	assert.Equal(t, "relay timeout", dao.calls[1].result)
	assert.Contains(t, string(dao.calls[1].metadata), `"exit_status":1`)
}

// TestCallToolGeneratesDistinctToolIds guards the upsert key: parallel calls to
// the same tool within one message collapse into a single row if they share a
// tool_id, which is exactly what resource_search's fan-out would produce.
func TestCallToolGeneratesDistinctToolIds(t *testing.T) {
	dao := installTrackingDao(t)
	tool := trackingStubTool{resp: toolcore.NBToolResponse{Data: "ok"}}

	_, _ = CallTool(trackingToolCtx(), tool, toolcore.NBToolCallRequest{Command: "a"})
	_, _ = CallTool(trackingToolCtx(), tool, toolcore.NBToolCallRequest{Command: "b"})

	require.Len(t, dao.calls, 4)
	assert.NotEqual(t, dao.calls[0].toolId, dao.calls[2].toolId)
}

// TestCallToolHonoursContextToolCallId verifies a caller-supplied id wins, so
// the tool and its persisted row agree (client-tool follow-ups look rows up by
// tool_id).
func TestCallToolHonoursContextToolCallId(t *testing.T) {
	dao := installTrackingDao(t)
	tc := trackingToolCtx()
	tc.ToolCallId = "caller-supplied"

	_, _ = CallTool(tc, trackingStubTool{}, toolcore.NBToolCallRequest{Command: "x"})

	require.Len(t, dao.calls, 2)
	assert.Equal(t, "caller-supplied", dao.calls[1].toolId)
}

// TestCallToolRecordsArgumentsOnlyRequests covers the mermaid-validation and
// logs-bundle shape, where the input lives in Arguments — persisting an empty
// parameters column would make those rows unreadable in the UI.
func TestCallToolRecordsArgumentsOnlyRequests(t *testing.T) {
	dao := installTrackingDao(t)

	_, _ = CallTool(trackingToolCtx(), trackingStubTool{},
		toolcore.NBToolCallRequest{Arguments: map[string]any{"code": "graph TD;"}})

	require.NotEmpty(t, dao.calls)
	assert.Contains(t, dao.calls[0].args, "graph TD;")
}
