package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dryRunToolContext() core.NbToolContext {
	return core.NbToolContext{
		AccountId: "acct-1",
		Ctx:       security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct-1"}),
	}
}

// withRunbookServer points the workflow tools at an httptest server for the test.
func withRunbookServer(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	prev := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() {
		config.Config.WorkflowServerEndpoint = prev
		srv.Close()
	})
}

// The whole gating model hinges on tools declaring their request type; a tool that
// implements neither inference interface silently defaults to read and skips
// confirmation + RBAC. Guard the classifications.
func TestWorkflowTools_InferToolRequestType(t *testing.T) {
	cases := []struct {
		tool core.ToolRequestInference
		want core.ToolRequestType
	}{
		{WorkflowTriggerTool{}, core.ToolRequestTypeCreate},
		{WorkflowExecutionRetriggerTool{}, core.ToolRequestTypeCreate},
		{WorkflowUpdateTool{}, core.ToolRequestTypeUpdate},
		{WorkflowConfigSaveTool{}, core.ToolRequestTypeUpdate},
		{WorkflowDryRunTool{}, core.ToolRequestTypeCreate},
	}
	for _, tc := range cases {
		got, err := tc.tool.InferToolRequestType(nil, "", "")
		assert.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
}

func TestWorkflowDryRunTool_WithDefinition(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	withRunbookServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"FAILED","tasks":[{"id":"a","type":"core.print","status":"COMPLETED"},{"id":"b","type":"core.http","status":"FAILED","error":"connection refused","rendered_params":{"url":"x"}}]}`))
	})

	def := map[string]any{"version": "v1", "triggers": []any{}, "tasks": []any{}}
	resp, err := WorkflowDryRunTool{}.Call(dryRunToolContext(), core.NBToolCallRequest{
		Arguments: map[string]any{"definition": def},
	})
	require.NoError(t, err)
	assert.Equal(t, "/workflows/dry-run", gotPath)
	assert.Equal(t, def, gotBody["definition"], "the inner definition must be forwarded under the definition key")

	// The summary surfaces overall status and the failing task's error, drops the noise.
	assert.Contains(t, resp.Data, `"status":"FAILED"`)
	assert.Contains(t, resp.Data, `"id":"b"`)
	assert.Contains(t, resp.Data, "connection refused")
}

func TestWorkflowDryRunTool_WithIdFetchesDefinition(t *testing.T) {
	var sawGet, sawDryRun bool
	withRunbookServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/workflows/wf-1"):
			sawGet = true
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"wf-1","name":"n","definition":{"version":"v1","tasks":[]}}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/workflows/dry-run"):
			sawDryRun = true
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			assert.NotNil(t, body["definition"], "definition resolved from the fetched workflow")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":"COMPLETED","tasks":[]}`))
		default:
			w.WriteHeader(404)
		}
	})

	resp, err := WorkflowDryRunTool{}.Call(dryRunToolContext(), core.NBToolCallRequest{
		Arguments: map[string]any{"id": "wf-1"},
	})
	require.NoError(t, err)
	assert.True(t, sawGet, "must fetch the workflow to resolve its definition")
	assert.True(t, sawDryRun, "must then dry-run")
	assert.Contains(t, resp.Data, `"status":"COMPLETED"`)
}

func TestWorkflowDryRunTool_RequiresIdOrDefinition(t *testing.T) {
	_, err := WorkflowDryRunTool{}.Call(dryRunToolContext(), core.NBToolCallRequest{Arguments: map[string]any{}})
	assert.Error(t, err)
}

func TestSummarizeDryRunResponse_TrimsToDiagnostics(t *testing.T) {
	raw := []byte(`{"status":"FAILED","inputs":{"x":1},"output":"big","tasks":[{"id":"a","type":"t","status":"COMPLETED","output":"noise"},{"id":"b","type":"t","status":"FAILED","error":"boom","rendered_params":{"p":1}}]}`)
	out := SummarizeDryRunResponse(raw)
	assert.Contains(t, out, `"status":"FAILED"`)
	assert.Contains(t, out, `"error":"boom"`)
	assert.Contains(t, out, `"rendered_params"`)
	// Successful task keeps no error/rendered_params noise.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	tasks := parsed["tasks"].([]any)
	a := tasks[0].(map[string]any)
	_, hasErr := a["error"]
	assert.False(t, hasErr, "completed task should not carry an error field")
}
