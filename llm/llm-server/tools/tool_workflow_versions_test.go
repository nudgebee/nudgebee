package tools

import (
	"net/http"
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowVersionTools_InferToolRequestType(t *testing.T) {
	cases := []struct {
		tool core.ToolRequestInference
		want core.ToolRequestType
	}{
		{WorkflowPublishTool{}, core.ToolRequestTypeUpdate},
		{WorkflowMakeVersionLiveTool{}, core.ToolRequestTypeUpdate},
		{WorkflowRestoreVersionTool{}, core.ToolRequestTypeUpdate},
		{WorkflowUpdateVersionMetaTool{}, core.ToolRequestTypeUpdate},
		{WorkflowDeleteVersionTool{}, core.ToolRequestTypeDelete},
	}
	for _, tc := range cases {
		got, err := tc.tool.InferToolRequestType(nil, "", "")
		assert.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
}

// Read-only version tools must NOT implement the inference interface (so they
// default to read and skip confirmation).
func TestWorkflowVersionReadTools_AreNotClassified(t *testing.T) {
	_, isInfer := any(WorkflowListVersionsTool{}).(core.ToolRequestInference)
	assert.False(t, isInfer, "list_versions must default to read")
	_, isInfer = any(WorkflowGetVersionTool{}).(core.ToolRequestInference)
	assert.False(t, isInfer, "get_version must default to read")
}

func TestWorkflowVersionTools_MethodAndPath(t *testing.T) {
	cases := []struct {
		name       string
		call       func(ctx core.NbToolContext) (core.NBToolResponse, error)
		wantMethod string
		wantPath   string
	}{
		{"list", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowListVersionsTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1"}})
		}, "GET", "/workflows/wf-1/versions"},
		{"get", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowGetVersionTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1", "version_number": float64(3)}})
		}, "GET", "/workflows/wf-1/versions/3"},
		{"publish", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowPublishTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1", "set_live": true}})
		}, "POST", "/workflows/wf-1/publish"},
		{"make_live", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowMakeVersionLiveTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1", "version_number": float64(2)}})
		}, "POST", "/workflows/wf-1/versions/2/make-live"},
		{"restore", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowRestoreVersionTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1", "version_number": float64(1)}})
		}, "POST", "/workflows/wf-1/versions/1/restore"},
		{"meta", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowUpdateVersionMetaTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1", "version_number": float64(2), "name": "v2"}})
		}, "PATCH", "/workflows/wf-1/versions/2"},
		{"delete", func(c core.NbToolContext) (core.NBToolResponse, error) {
			return WorkflowDeleteVersionTool{}.Call(c, core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1", "version_number": float64(4)}})
		}, "DELETE", "/workflows/wf-1/versions/4"},
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

func TestWorkflowVersionTools_RequireArgs(t *testing.T) {
	_, err := WorkflowGetVersionTool{}.Call(dryRunToolContext(), core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1"}})
	assert.Error(t, err, "missing version_number")
	_, err = WorkflowPublishTool{}.Call(dryRunToolContext(), core.NBToolCallRequest{Arguments: map[string]any{}})
	assert.Error(t, err, "missing id")
}
