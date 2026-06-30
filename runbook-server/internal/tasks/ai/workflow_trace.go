package ai

import (
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/llm"
)

// workflowTraceFields returns the SessionId / WorkflowId / ExecutionId / Labels
// to stamp on every llm.LLMRequest dispatched from a workflow task. Centralised
// so all four LLM tasks emit the same trace fields and llm-server logs/audit
// rows can be grouped by workflow run.
//
// SessionId format: `wf__<workflow-id>__<workflow-run-id>`. Keeping
// workflow-run-id (Temporal's per-execution id) as the last segment preserves
// the existing resume semantics — each run gets its own llm conversation —
// while the `wf__` prefix and embedded workflow id make the identifier
// self-describing in llm-server logs.
func workflowTraceFields(taskCtx types.TaskContext) (sessionId string, workflowId string, executionId string, labels map[string]any) {
	workflowId = taskCtx.GetWorkflowID()
	executionId = taskCtx.GetWorkflowRunID()
	sessionId = fmt.Sprintf("wf__%s__%s", workflowId, executionId)

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
// session id keeps it.
func applyWorkflowTrace(taskCtx types.TaskContext, req llm.LLMRequest) llm.LLMRequest {
	sessionId, workflowId, executionId, labels := workflowTraceFields(taskCtx)
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
