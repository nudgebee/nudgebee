package tools

import (
	"net/http"
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowLifecycleTools_InferToolRequestType(t *testing.T) {
	cases := []struct {
		tool core.ToolRequestInference
		want core.ToolRequestType
	}{
		{WorkflowPauseTool{}, core.ToolRequestTypeUpdate},
		{WorkflowResumeTool{}, core.ToolRequestTypeUpdate},
		{WorkflowDeleteTool{}, core.ToolRequestTypeDelete},
		{WorkflowExecutionCancelTool{}, core.ToolRequestTypeDelete},
		{WorkflowTaskExecuteTool{}, core.ToolRequestTypeCreate},
		{WorkflowConfigDeleteTool{}, core.ToolRequestTypeDelete},
	}
	for _, tc := range cases {
		got, err := tc.tool.InferToolRequestType(nil, "", "")
		assert.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
}

func TestWorkflowLifecycleTools_MethodAndPath(t *testing.T) {
	cases := []struct {
		name       string
		call       func(ctx core.NbToolContext) (core.NBToolResponse, error)
		wantMethod string
		wantPath   string
	}{
		{"pause", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowPauseTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1"}})
		}, "POST", "/workflows/wf-1/pause"},
		{"resume", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowResumeTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1"}})
		}, "POST", "/workflows/wf-1/resume"},
		{"delete", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowDeleteTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1"}})
		}, "DELETE", "/workflows/wf-1"},
		{"cancel", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowExecutionCancelTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1", "execution_id": "e-9"}})
		}, "POST", "/workflows/wf-1/executions/e-9/cancel"},
		{"task_execute", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowTaskExecuteTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"task_type": "core.print", "params": map[string]any{"message": "hi"}}})
		}, "POST", "/tasks/core.print/execute"},
		{"config_delete", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowConfigDeleteTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"key": "slack_channel"}})
		}, "DELETE", "/configs/slack_channel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath string
			withRunbookServer(t, func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
			_, err := tc.call(dryRunToolContext())
			require.NoError(t, err)
			assert.Equal(t, tc.wantMethod, gotMethod)
			assert.Equal(t, tc.wantPath, gotPath)
		})
	}
}

func TestWorkflowLifecycleTools_RequireArgs(t *testing.T) {
	_, err := WorkflowPauseTool{}.Call(dryRunToolContext(), core.NBToolCallRequest{Arguments: map[string]any{}})
	assert.Error(t, err)
	_, err = WorkflowExecutionCancelTool{}.Call(dryRunToolContext(), core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1"}})
	assert.Error(t, err, "missing execution_id")
	_, err = WorkflowConfigDeleteTool{}.Call(dryRunToolContext(), core.NBToolCallRequest{Arguments: map[string]any{}})
	assert.Error(t, err, "missing key")
}
