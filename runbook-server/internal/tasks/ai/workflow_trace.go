package ai

import (
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/llm"
	"strings"

	"github.com/google/uuid"
)

// sessionScopeParamFieldName is the schema key for the optional LLM session
// scope. Kept in one place so the conversational LLM tasks (investigate,
// summary, nubi) read/write the same key.
const sessionScopeParamFieldName = "session_scope"

const (
	// sessionScopeExecution (default) gives every workflow run its own LLM
	// conversation: session id `wf__<workflow-id>__<workflow-run-id>`.
	sessionScopeExecution = "execution"
	// sessionScopeWorkflow shares one LLM conversation across all runs of the
	// workflow: session id `wf__<workflow-id>`. llm-server resumes a
	// conversation by (account_id, session_id) (lookup in api/chains.go), so
	// every run appends its messages to the same conversation instead of
	// starting fresh.
	sessionScopeWorkflow = "workflow"
)

// workflowTraceFields returns the SessionId / WorkflowId / ExecutionId / Labels
// to stamp on every llm.LLMRequest dispatched from a workflow task. Centralised
// so all four LLM tasks emit the same trace fields and llm-server logs/audit
// rows can be grouped by workflow run.
//
// SessionId format depends on sessionScope:
//   - sessionScopeExecution (or ""): `wf__<workflow-id>__<workflow-run-id>`.
//     Keeping workflow-run-id (Temporal's per-execution id) as the last segment
//     gives each run its own llm conversation, while the `wf__` prefix and
//     embedded workflow id make the identifier self-describing in llm-server logs.
//   - sessionScopeWorkflow: `wf__<workflow-id>`. All runs of the workflow share
//     one llm conversation, so successive executions append to the same history.
//
// WorkflowId / ExecutionId / Labels are always per-run regardless of scope —
// only the conversation identity changes.
//
// An isolated "Run Task" execution (Task Runner) has neither a workflow id nor
// a run id, so both formats above collapse to one constant (`wf____` / `wf__`)
// and every Task Runner run in the account lands in the same llm-server
// conversation, carrying earlier runs' text into the next one (#39834). Such a
// run gets `wf__runtask__<uuid>` whatever the scope: there is no workflow to
// share a conversation across. A random id is safe here and only here —
// ExecuteTask is a direct synchronous call, so nothing re-rolls it; inside an
// activity it would change on every Temporal retry. For the same reason the
// workflow scope applies only when there is a workflow id; without one it
// falls back to the per-run id instead of the constant `wf__`.
func workflowTraceFields(taskCtx types.TaskContext, sessionScope string) (sessionId string, workflowId string, executionId string, labels map[string]any) {
	workflowId = taskCtx.GetWorkflowID()
	executionId = taskCtx.GetWorkflowRunID()
	switch {
	case workflowId == "" && executionId == "":
		sessionId = "wf__runtask__" + uuid.NewString()
	case sessionScope == sessionScopeWorkflow && workflowId != "":
		sessionId = fmt.Sprintf("wf__%s", workflowId)
	default:
		sessionId = fmt.Sprintf("wf__%s__%s", workflowId, executionId)
	}

	labels = map[string]any{}
	if name := taskCtx.GetWorkflowName(); name != "" {
		labels["workflow_name"] = name
	}
	if id := taskCtx.GetTaskID(); id != "" {
		labels["task_id"] = id
	}
	if len(labels) == 0 {
		labels = nil
	}
	return
}

// applyWorkflowTrace stamps SessionId/WorkflowId/ExecutionId/Labels on req from
// taskCtx and returns the augmented request. Caller-supplied fields are left
// untouched (only zero values are filled), so a task that already set a custom
// session id keeps it. sessionScope selects the SessionId format (see
// workflowTraceFields); pass "" or sessionScopeExecution for the default
// per-run conversation.
func applyWorkflowTrace(taskCtx types.TaskContext, sessionScope string, req llm.LLMRequest) llm.LLMRequest {
	sessionId, workflowId, executionId, labels := workflowTraceFields(taskCtx, sessionScope)
	if req.SessionId == "" {
		req.SessionId = sessionId
	}
	if req.WorkflowId == "" {
		req.WorkflowId = workflowId
	}
	if req.ExecutionId == "" {
		req.ExecutionId = executionId
	}
	if req.Labels == nil {
		req.Labels = labels
	}
	return req
}

// parseSessionScopeParam normalises the optional session_scope parameter.
// Empty / nil input returns sessionScopeExecution (the historical behaviour);
// whitespace and casing are normalised since the value may come from a
// hand-edited workflow definition rather than the UI dropdown. Anything other
// than the two known scopes is rejected loudly so a typo in a workflow
// definition doesn't silently fall back to a fresh conversation.
func parseSessionScopeParam(raw any) (string, error) {
	if raw == nil {
		return sessionScopeExecution, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s parameter must be a string, got %T", sessionScopeParamFieldName, raw)
	}
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", sessionScopeExecution:
		return sessionScopeExecution, nil
	case sessionScopeWorkflow:
		return sessionScopeWorkflow, nil
	default:
		return "", fmt.Errorf("%s parameter must be %q or %q, got %q", sessionScopeParamFieldName, sessionScopeExecution, sessionScopeWorkflow, s)
	}
}

// sessionScopeInputSchemaProperty returns the shared Property used by the
// conversational LLM tasks to expose the session scope picker.
func sessionScopeInputSchemaProperty(order int) types.Property {
	return types.Property{
		Type:        types.PropertyTypeString,
		Title:       "Conversation Scope",
		Description: "'execution' (default) starts a fresh AI conversation on every run. 'workflow' reuses one conversation shared by all runs of this workflow, so each run appends to the same history and the AI can see previous runs' context.",
		Help: "With scope 'workflow' the conversation session id is `wf__<workflow-id>` instead of `wf__<workflow-id>__<execution-id>`, " +
			"so successive executions resume the same conversation on the LLM server. " +
			"Useful for recurring workflows that should remember earlier findings. " +
			"A Task Runner run is not part of a workflow, so it always starts a fresh conversation. " +
			"Caveats: the shared history grows with every run (older context may eventually be truncated by the model's context window), " +
			"and concurrent executions of the same workflow will interleave their messages in the one conversation.",
		Required: false,
		Default:  sessionScopeExecution,
		Options:  []string{sessionScopeExecution, sessionScopeWorkflow},
		Order:    order,
	}
}
