package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// gateBody is the body runbook-server's REST trigger handler answers an AI
// gate refusal with (api/handlers.go: gin.H{"error": commonErr.Message}).
func gateBody(code string) string {
	return fmt.Sprintf(`{"error":"workflow is not available to the AI assistant (%s)"}`, code)
}

func TestRunNotStartedResponseWordsEachRefusal(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"paused", http.StatusForbidden, gateBody("not_active"), "it is not ACTIVE (it is paused or disabled)"},
		{"not opted in", http.StatusForbidden, gateBody("not_opted_in"), "not enabled for the AI assistant"},
		{"feature off", http.StatusForbidden, gateBody("not_enabled"), "not enabled for this account"},
		{"no manual trigger", http.StatusForbidden, gateBody("no_manual_trigger"), "no manual trigger"},
		{"no user", http.StatusForbidden, gateBody("no_user"), "no identified user"},
		{"not found", http.StatusNotFound, `{"error":"workflow not found"}`, "the automation was not found"},
		{"other 403", http.StatusForbidden, `{"error":"you cannot run workflows"}`,
			"the automation server refused the request (status 403): you cannot run workflows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Wrapped, as automationTool.runAndWait wraps it.
			err := fmt.Errorf("failed to run automation %q: %w", "Restart Pods",
				&runbookStatusError{status: tc.status, body: tc.body})

			resp, ok := runNotStartedResponse(`"Restart Pods"`, err)
			require.True(t, ok)
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
			assert.True(t, strings.HasPrefix(resp.Data, runNotStartedPrefix), resp.Data)
			assert.Contains(t, resp.Data, tc.want)
			assert.Contains(t, resp.Data, "there is no execution ID")

			statement, ok := RunNotStartedStatement(resp.Data)
			require.True(t, ok)
			assert.True(t, strings.HasPrefix(statement, `"Restart Pods" was not run: `), statement)
			assert.Contains(t, statement, tc.want)
			assert.True(t, strings.HasSuffix(statement, "."), statement)
			assert.NotContains(t, statement, "Do not retry", "the statement must not carry the model guidance")
		})
	}
}

// Only an answer from runbook-server says no run exists. A failure before the
// request (a bad argument) or of the transport keeps its own error.
func TestRunNotStartedResponseIgnoresOtherErrors(t *testing.T) {
	_, ok := runNotStartedResponse(`"X"`, errors.New("failed to send request: connection refused"))
	assert.False(t, ok)
	// A 5xx can come from a proxy after the run was created.
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout} {
		_, ok = runNotStartedResponse(`"X"`, &runbookStatusError{status: status, body: `{"error":"upstream"}`})
		assert.False(t, ok, status)
	}
	_, ok = runNotStartedResponse(`"X"`, nil)
	assert.False(t, ok)
}

func TestRunNotStartedStatementRejectsOtherObservations(t *testing.T) {
	_, ok := RunNotStartedStatement(`{"workflow_id":"wf-1","execution_id":"e-1","status":"COMPLETED"}`)
	assert.False(t, ok)
	_, ok = RunNotStartedStatement("runbook server returned error (status 403): {}")
	assert.False(t, ok)
}

// The run record lists a turn's run tool calls by name. A tool that starts runs
// but is not matched here would silently drop out of every record.
func TestIsRunToolMatchesEveryRunTool(t *testing.T) {
	for _, name := range RunToolNames() {
		assert.True(t, IsRunTool(name), name)
	}
	assert.True(t, IsRunTool(ToolWorkflowTrigger))
	assert.True(t, IsRunTool(ToolWorkflowExecutionRetrigger))

	built := buildAutomationTools("acct", []automationCandidate{
		{ID: "wf-1", Name: "Restart Pods", LLMDescription: "Use when pods crashloop."},
		{ID: "wf-2", Name: "V2 - cross-cloud-vm-service-restart", LLMDescription: "Restarts a service."},
	})
	require.Len(t, built, 2)
	for _, tool := range built {
		assert.True(t, IsRunTool(tool.Name()), tool.Name())
		assert.True(t, strings.HasPrefix(tool.Name(), RunToolPrefix()), tool.Name())
	}

	// A built-in tool or agent that shares the prefix is not a run.
	core.RegisterNBToolFactory("automation_builder_for_test", func(string) (core.NBTool, error) { return nil, nil })
	assert.False(t, IsRunTool("automation_builder_for_test"))

	for _, name := range []string{ToolWorkflowResume, ToolWorkflowSearch, ToolWorkflowList, ToolWorkflowGet, ToolWorkflowExecutionGet, "automation"} {
		assert.False(t, IsRunTool(name), name)
	}
}

// A refused trigger must reach the model as a result that says no run
// started, not as the raw server error: the raw error was followed by replies
// reporting a COMPLETED run that never existed.
func TestAutomationToolReportsARefusalAsNoRunStarted(t *testing.T) {
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/trigger") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, gateBody("not_active"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})

	tool := &automationTool{accountId: "acct", id: "wf-1", name: "Restart Pods",
		toolName: "automation_restart_pods", reasonArg: defaultReasonArg}
	resp, err := tool.Call(testRunToolContext(), core.NBToolCallRequest{Arguments: map[string]any{
		defaultReasonArg: "the user asked for this",
	}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.True(t, strings.HasPrefix(resp.Data, `NO RUN STARTED: "Restart Pods" was not run: it is not ACTIVE`), resp.Data)
}

func TestWorkflowTriggerReportsARefusalAsNoRunStarted(t *testing.T) {
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/trigger"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, gateBody("not_opted_in"))
		case strings.Contains(r.URL.Path, "ai-search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"workflows": []map[string]any{
				{"id": "wf-1", "name": "Restart Pods", "llm_description": "Use when pods crashloop."},
			}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	})

	resp, err := WorkflowTriggerTool{accountId: "acct"}.Call(testRunToolContext(),
		core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-1"}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.True(t, strings.HasPrefix(resp.Data, `NO RUN STARTED: "Restart Pods" was not run: it is not enabled for the AI assistant`), resp.Data)
}

// A paused automation is not among those the assistant may run, so its name
// is read from runbook-server for the refusal.
func TestWorkflowTriggerNamesARefusedAutomationItMayNotRun(t *testing.T) {
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/trigger"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, gateBody("not_active"))
		case strings.HasSuffix(r.URL.Path, "/workflows/wf-paused"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "wf-paused", "name": "Nightly Cleanup"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"workflows": []map[string]any{}})
		}
	})

	resp, err := WorkflowTriggerTool{accountId: "acct"}.Call(testRunToolContext(),
		core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-paused"}})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(resp.Data, `NO RUN STARTED: "Nightly Cleanup" was not run: it is not ACTIVE`), resp.Data)
}

func TestWorkflowResumeSaysItDidNotRun(t *testing.T) {
	out := withResumeNote([]byte(`{"message":"workflow resumed successfully"}`))
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	assert.Equal(t, resumeNote, parsed["note"])
	assert.Equal(t, map[string]any{"message": "workflow resumed successfully"}, parsed["result"])
	assert.Contains(t, resumeNote, "no execution was created")

	assert.Contains(t, withResumeNote([]byte("ok")), `"result":"ok"`)
}

func TestWorkflowSearchNotesAnEmptyResult(t *testing.T) {
	for _, body := range []string{`{"workflows":[],"total_invocable":0}`, `{"workflows":null}`, `{}`} {
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(withNoMatchNote([]byte(body)), &parsed), body)
		assert.Equal(t, noMatchNote, parsed["note"], body)
	}

	found := []byte(`{"workflows":[{"id":"wf-1"}]}`)
	assert.Equal(t, found, withNoMatchNote(found))
	assert.Equal(t, []byte("not json"), withNoMatchNote([]byte("not json")))
}

func TestAutomationToolDescriptionAsksToNameTheAutomationBeforeASubstitute(t *testing.T) {
	for _, autoApprove := range []bool{false, true} {
		tool := &automationTool{name: "cross-cloud-vm-service-restart", description: "Restarts a service.", autoApprove: autoApprove}
		assert.Contains(t, tool.Description(),
			`If the user asked for a different automation or version than "cross-cloud-vm-service-restart", tell them this is the one that will run before calling it.`)
	}
}

func TestAutomationRunNameForEachRunTool(t *testing.T) {
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"workflows": []map[string]any{
			{"id": "wf-1", "name": "Restart Pods", "llm_description": "Use when pods crashloop."},
		}})
	})

	assert.Equal(t, `"Restart Pods"`, AutomationRunName("acct", "automation_restart_pods", `{"reason":"x"}`))
	assert.Equal(t, `"Restart Pods"`, AutomationRunName("acct", ToolWorkflowTrigger, `{"id":"wf-1"}`))
	assert.Equal(t, "the automation with ID wf-9", AutomationRunName("acct", ToolWorkflowTrigger, `{"id":"wf-9"}`))
	assert.Equal(t, `"automation_gone"`, AutomationRunName("acct", "automation_gone", `{}`))
	assert.Equal(t, `"workflow_trigger"`, AutomationRunName("acct", ToolWorkflowTrigger, `not json`))
}

func testRunToolContext() core.NbToolContext {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})
	return core.NbToolContext{
		AccountId: "acct",
		Ctx:       security.NewRequestContext(context.Background(), secCtx, logger, nil, nil),
	}
}
