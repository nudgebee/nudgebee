package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	toolcore "nudgebee/llm/tools/core"
)

func TestShellTargetUnresolved(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"kubectl naming its context", `{"command":"kubectl --context rackspace get deployments -A"}`, false},
		{"kubectl naming no context", `{"command":"kubectl get deployments -A"}`, true},
		{"aws naming its profile", `{"command":"aws sqs list-queues --profile aws-prod"}`, false},
		{"context chosen by a shell variable", `{"command":"for c in a b; do kubectl --context $c get pods; done"}`, true},
		{"command with no cloud CLI", `{"command":"ls -la"}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := NBAgentPlannerToolAction{Tool: toolcore.ToolExecuteShellCommand, ToolInput: tc.input}
			assert.Equal(t, tc.want, shellTargetUnresolved(nil, action))
		})
	}
}

type capturingToolCallback struct {
	before    []NBAgentPlannerToolAction
	after     []NBAgentPlannerToolAction
	responses []toolcore.NBToolResponse
}

func (c *capturingToolCallback) BeforeToolCall(toolcall NBAgentPlannerToolAction) {
	c.before = append(c.before, toolcall)
}

func (c *capturingToolCallback) AfterToolCallResponse(toolcall NBAgentPlannerToolAction, response toolcore.NBToolResponse) {
	c.after = append(c.after, toolcall)
	c.responses = append(c.responses, response)
}

func TestRecordRefusedCall_WritesFailedToolCallRow(t *testing.T) {
	callback := &capturingToolCallback{}
	e := &plannerExecutor{toolCallbackHandler: callback}
	action := NBAgentPlannerToolAction{ToolID: "shell_execute-abc-E4-1", Tool: toolcore.ToolExecuteShellCommand, ToolInput: `{"command":"kubectl get deployments -A"}`}

	e.recordRefusedCall(NBAgentPlannerToolActionStep{Action: action, Observation: "name the cluster", Status: ToolStatusFailure})

	require.Len(t, callback.before, 1)
	require.Len(t, callback.after, 1)
	assert.Equal(t, action, callback.before[0])
	assert.Equal(t, action, callback.after[0])
	assert.Equal(t, toolcore.NBToolResponseStatusError, callback.responses[0].Status)
	assert.Equal(t, "name the cluster", callback.responses[0].Data)
}

func TestRecordRefusedCall_SkipsStepWithoutToolID(t *testing.T) {
	callback := &capturingToolCallback{}
	e := &plannerExecutor{toolCallbackHandler: callback}

	e.recordRefusedCall(NBAgentPlannerToolActionStep{Observation: "refused", Status: ToolStatusFailure})

	assert.Empty(t, callback.before)
	assert.Empty(t, callback.after)
}

// toolCallRowDao records the tool-call rows written through it.
type toolCallRowDao struct {
	fakeConversationDao
	rows []toolCallRow
}

type toolCallRow struct {
	toolID   string
	response string
	status   toolcore.NBToolResponseStatus
}

func (d *toolCallRowDao) SaveConversationToolCall(_, _, _, _, _, toolId, _, _, _, _, toolResult string, status toolcore.NBToolResponseStatus, _ toolcore.NBToolType, _ *string, _ []toolcore.NBToolResponseReference, _, _ []byte) error {
	d.rows = append(d.rows, toolCallRow{toolID: toolId, response: toolResult, status: status})
	return nil
}

func (*toolCallRowDao) GetConversationAgentInput(_, _ string) (string, error) { return "", nil }

// A write refused by a read-only run still leaves a failed tool-call row.
func TestDoAction_ReadOnlyRefusalWritesToolCallRow(t *testing.T) {
	executor, nameToTool, action, dao := readOnlyWriteExecutor(t)

	step, finish, err := executor.doAction(nameToTool, action, "")
	require.NoError(t, err)
	require.Nil(t, finish)
	assert.Equal(t, ToolStatusFailure, step.Status)

	require.NotEmpty(t, dao.rows)
	last := dao.rows[len(dao.rows)-1]
	assert.Equal(t, "probe", last.toolID)
	assert.Equal(t, toolcore.NBToolResponseStatusError, last.status)
	assert.Equal(t, step.Observation, last.response)
}
