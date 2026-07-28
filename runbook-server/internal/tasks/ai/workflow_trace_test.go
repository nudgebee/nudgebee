package ai

import (
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/llm"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSessionScopeParam(t *testing.T) {
	tests := []struct {
		name      string
		input     any
		want      string
		expectErr bool
	}{
		{name: "nil defaults to execution", input: nil, want: sessionScopeExecution},
		{name: "empty string defaults to execution", input: "", want: sessionScopeExecution},
		{name: "execution passes through", input: "execution", want: sessionScopeExecution},
		{name: "workflow passes through", input: "workflow", want: sessionScopeWorkflow},
		{name: "whitespace is trimmed", input: "  workflow  ", want: sessionScopeWorkflow},
		{name: "casing is normalised", input: "Workflow", want: sessionScopeWorkflow},
		{name: "unknown value errors", input: "run", expectErr: true},
		{name: "non-string errors", input: 42, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSessionScopeParam(tc.input)
			if tc.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWorkflowTraceFields_SessionScope(t *testing.T) {
	taskCtx := &testutils.MockTaskContext{
		WfID:          "wfid",
		WorkflowRunId: "runid",
		WfName:        "my-workflow",
		TskID:         "task-1",
	}

	t.Run("execution scope embeds the run id", func(t *testing.T) {
		sessionId, workflowId, executionId, labels := workflowTraceFields(taskCtx, sessionScopeExecution)
		assert.Equal(t, "wf__wfid__runid", sessionId)
		assert.Equal(t, "wfid", workflowId)
		assert.Equal(t, "runid", executionId)
		assert.Equal(t, map[string]any{"workflow_name": "my-workflow", "task_id": "task-1"}, labels)
	})

	t.Run("empty scope behaves like execution", func(t *testing.T) {
		sessionId, _, _, _ := workflowTraceFields(taskCtx, "")
		assert.Equal(t, "wf__wfid__runid", sessionId)
	})

	t.Run("workflow scope drops the run id", func(t *testing.T) {
		sessionId, workflowId, executionId, _ := workflowTraceFields(taskCtx, sessionScopeWorkflow)
		assert.Equal(t, "wf__wfid", sessionId)
		// Trace fields stay per-run — only the conversation identity is shared.
		assert.Equal(t, "wfid", workflowId)
		assert.Equal(t, "runid", executionId)
	})
}

func TestApplyWorkflowTrace_SessionScope(t *testing.T) {
	taskCtx := &testutils.MockTaskContext{WfID: "wfid", WorkflowRunId: "runid"}

	t.Run("workflow scope stamps the shared session id", func(t *testing.T) {
		req := applyWorkflowTrace(taskCtx, sessionScopeWorkflow, llm.LLMRequest{})
		assert.Equal(t, "wf__wfid", req.SessionId)
		assert.Equal(t, "wfid", req.WorkflowId)
		assert.Equal(t, "runid", req.ExecutionId)
	})

	t.Run("caller-supplied session id is preserved", func(t *testing.T) {
		req := applyWorkflowTrace(taskCtx, sessionScopeWorkflow, llm.LLMRequest{SessionId: "custom"})
		assert.Equal(t, "custom", req.SessionId)
	})
}

func TestLLMTasks_InputSchema_SessionScopeField(t *testing.T) {
	schemas := map[string]*types.Schema{
		"llm.investigate": (&LLMInvestigateTask{}).InputSchema(),
		"llm.summary":     (&LLMSummaryTask{}).InputSchema(),
		"llm.nubi":        (&LLMNubiTask{}).InputSchema(),
	}

	for name, schema := range schemas {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, schema.Properties, sessionScopeParamFieldName)
			prop := schema.Properties[sessionScopeParamFieldName]
			assert.False(t, prop.Required, "session_scope should be optional")
			assert.Equal(t, sessionScopeExecution, prop.Default)
			assert.Equal(t, []string{sessionScopeExecution, sessionScopeWorkflow}, prop.Options)
		})
	}

	// The classifier is one-shot by design: sharing history across runs could
	// skew classifications, so it must not expose the knob.
	classify := (&LLMRouterClassifyTask{}).InputSchema()
	assert.NotContains(t, classify.Properties, sessionScopeParamFieldName)
}
