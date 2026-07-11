package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

func dryRunBuilderCtx() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct-1"})
}

// toolDryRun must POST the inner definition to workflows/dry-run and surface the
// per-task result so the editor assistant can verify/diagnose with real execution.
func TestWorkflowBuilderAgent_ToolDryRun(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"FAILED","tasks":[{"id":"a","type":"core.print","status":"COMPLETED"},{"id":"categorize","type":"scripting.run_script","status":"FAILED","error":"argument list too long","rendered_params":{"x":1}}]}`))
	}))
	defer srv.Close()
	prev := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	defer func() { config.Config.WorkflowServerEndpoint = prev }()

	agent := newWorkflowBuilderAgent("acct-1")
	innerDef := map[string]interface{}{"version": "v1", "triggers": []interface{}{}, "tasks": []interface{}{}}
	agent.state.WorkingWorkflow = map[string]interface{}{"name": "cf-stack-cleanup", "definition": innerDef}

	out := agent.toolDryRun(dryRunBuilderCtx())

	assert.Equal(t, "/workflows/dry-run", gotPath)
	assert.Equal(t, innerDef, gotBody["definition"], "the inner definition must be sent under the definition key")
	assert.Equal(t, "cf-stack-cleanup", gotBody["name"])

	// Surfaces overall status + the failing task's error for diagnosis.
	assert.Contains(t, out, "Dry-run complete")
	assert.Contains(t, out, `"status":"FAILED"`)
	assert.Contains(t, out, `"id":"categorize"`)
	assert.Contains(t, out, "argument list too long")
}

func TestWorkflowBuilderAgent_ToolDryRun_NotInitialized(t *testing.T) {
	agent := newWorkflowBuilderAgent("acct-1")
	out := agent.toolDryRun(dryRunBuilderCtx())
	assert.Contains(t, out, "not initialized")
}
