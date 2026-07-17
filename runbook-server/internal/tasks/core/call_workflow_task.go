package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model" // Updated import to model
	"nudgebee/runbook/internal/tasks/types"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
)

// resolveTargetVersion picks which snapshot of the callee to run. When the task
// config carries a positive integer `workflow_version`, it pins to that specific
// historical version (GetWorkflowVersion); otherwise it follows the callee's
// floating Live pointer (GetLiveWorkflowVersion). The Live fallback is the
// backwards-compatible default for every existing Call Workflow action — absent
// param == today's behavior, so no migration is needed (#282).
func resolveTargetVersion(ctx context.Context, store model.WorkflowStore, workflowID, workflowName string, params map[string]any) (*model.WorkflowVersion, error) {
	if pinned, ok := parseWorkflowVersionParam(params["workflow_version"]); ok {
		v, err := store.GetWorkflowVersion(ctx, workflowID, pinned)
		if err != nil {
			return nil, fmt.Errorf("failed to load pinned version v%d of workflow '%s': %w", pinned, workflowName, err)
		}
		// A store may return (nil, nil) on a not-found row; guard so the caller
		// never dereferences a nil version (.Definition) and panics.
		if v == nil {
			return nil, fmt.Errorf("pinned version v%d of workflow '%s' not found", pinned, workflowName)
		}
		return v, nil
	}
	v, err := store.GetLiveWorkflowVersion(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to load live version of workflow '%s': %w", workflowName, err)
	}
	if v == nil {
		return nil, fmt.Errorf("live version of workflow '%s' not found", workflowName)
	}
	return v, nil
}

// parseWorkflowVersionParam coerces the JSON-decoded `workflow_version` task
// param into a positive version number. Returns (0,false) when absent, zero,
// negative, fractional, or non-numeric — the caller then falls back to Live.
// Numbers arrive as float64 from JSON config; we also accept int/int64/json.Number
// for programmatic callers. Strings are coerced too: a templated param
// (`workflow_version: "{{ Inputs.target_version }}"`) resolves to a string like
// "3", so without this a pinned call would silently fall back to Live.
func parseWorkflowVersionParam(raw any) (int, bool) {
	switch n := raw.(type) {
	case int:
		if n > 0 {
			return n, true
		}
	case int64:
		if n > 0 {
			return int(n), true
		}
	case float64:
		if n > 0 && n == math.Trunc(n) {
			return int(n), true
		}
	case json.Number:
		if i, err := n.Int64(); err == nil && i > 0 {
			return int(i), true
		}
	case string:
		if i, err := strconv.Atoi(n); err == nil && i > 0 {
			return i, true
		}
	}
	return 0, false
}

// CallWorkflowTask implements the Task interface for executing another workflow.
type CallWorkflowTask struct {
}

// Compile-time interface assertions. CallWorkflowTask MUST satisfy both
// TaskInlineWorkflow and TaskExecutionStrategy — losing either drops the task
// out of the executor's inline branch (executor.go:1156) and it would get
// dispatched as a regular Temporal activity, where Execute() below returns an
// error. Matches the pattern used by GroupTask.
var (
	_ types.TaskInlineWorkflow    = (*CallWorkflowTask)(nil)
	_ types.TaskExecutionStrategy = (*CallWorkflowTask)(nil)
)

func (t *CallWorkflowTask) GetName() string {
	return "core.call-workflow"
}

// GetDescription returns a brief description of the task.
func (t *CallWorkflowTask) GetDescription() string {
	return "Run another workflow by name and return its result."
}

// GetDisplayName returns a human-readable name for the task.
func (t *CallWorkflowTask) GetDisplayName() string {
	return "Call Workflow"
}

// Execute runs the target workflow synchronously via the Temporal client and returns
// its result. This is a fallback path: the executor SHOULD detect this task as
// TaskInlineWorkflow + ShouldExecuteAsChildWorkflow=true (see executor.go:1156-1217)
// and dispatch a proper Temporal child workflow with parent-child linkage. If we
// land here it means the inline path was skipped (e.g. stale binary, replay against
// pre-fix history). We log loudly so the underlying bug is visible, but still
// execute the workflow so the user isn't blocked. Trade-off: this path starts a
// top-level Temporal workflow instead of a child, so the execution view won't show
// drill-down into the called workflow's tasks under this node.
func (t *CallWorkflowTask) Execute(taskCtx types.TaskContext, params map[string]any) (result any, err error) {
	logger := taskCtx.GetLogger()
	logger.Warn("CallWorkflowTask.Execute called via activity dispatch — falling back to client-side workflow trigger. The executor's inline branch should have handled this; investigate why it didn't.",
		"workflowID", taskCtx.GetWorkflowID(), "taskID", taskCtx.GetTaskID())

	workflowName, ok := params["workflow_name"].(string)
	if !ok || workflowName == "" {
		return nil, fmt.Errorf("missing or invalid 'workflow_name' parameter for core.call-workflow task")
	}

	tenantID := taskCtx.GetTenantID()
	accountID := taskCtx.GetAccountID()
	if tenantID == "" || accountID == "" {
		return nil, fmt.Errorf("tenantID or accountID missing from TaskContext for core.call-workflow task")
	}

	// DryRun short-circuit: don't actually start a workflow; return a placeholder
	// that satisfies OutputSchema so downstream tasks can render values.
	if taskCtx.IsDryRun() {
		return map[string]any{
			"workflow_id": fmt.Sprintf("dry-run-%s", workflowName),
			"run_id":      fmt.Sprintf("dry-run-%s", uuid.New().String()),
			"output":      map[string]any{},
		}, nil
	}

	temporalClient := taskCtx.GetTemporalClient()
	if temporalClient == nil {
		return nil, fmt.Errorf("temporal client unavailable in TaskContext for core.call-workflow fallback")
	}

	// Recursion guard. Read the parent workflow's Memo for the call-workflow
	// depth counter (set by the executor's inline path on every previous
	// core.call-workflow boundary). Refuse to spawn past MaxCallWorkflowDepth
	// so a cyclic call chain (Wf A -> Wf B -> Wf A) can't blow up the cluster
	// through the fallback path. activity.GetInfo() gives the Temporal IDs
	// (the IDs stored in taskCtx are the user's definition IDs, not what
	// DescribeWorkflowExecution accepts).
	callWfDepth := int64(0)
	if activityInfo := activity.GetInfo(taskCtx.GetContext()); activityInfo.WorkflowExecution.ID != "" {
		descResp, descErr := temporalClient.DescribeWorkflowExecution(
			taskCtx.GetContext(),
			activityInfo.WorkflowExecution.ID,
			activityInfo.WorkflowExecution.RunID,
		)
		if descErr == nil && descResp.WorkflowExecutionInfo != nil && descResp.WorkflowExecutionInfo.Memo != nil {
			if payload, ok := descResp.WorkflowExecutionInfo.Memo.GetFields()[types.MemoKeyCallWorkflowDepth]; ok {
				_ = taskCtx.GetDataConverter().FromPayload(payload, &callWfDepth)
			}
		}
	}
	if callWfDepth >= int64(types.MaxCallWorkflowDepth) {
		return nil, fmt.Errorf(
			"core.call-workflow recursion depth (%d) exceeded; check for a cycle in your workflow call chain",
			types.MaxCallWorkflowDepth,
		)
	}

	// Look up the target workflow, then resolve the version to run — the pinned
	// version when the config specifies one, else the callee's floating LIVE
	// version (never its draft) (H2, #282). The fallback start must run the same
	// snapshot the inline child path would have run.
	targetWf, err := taskCtx.GetStore().FindByName(taskCtx.GetContext(), tenantID, accountID, workflowName)
	if err != nil {
		return nil, fmt.Errorf("failed to find workflow '%s': %w", workflowName, err)
	}
	if targetWf == nil {
		return nil, fmt.Errorf("workflow '%s' not found", workflowName)
	}
	targetVersion, err := resolveTargetVersion(taskCtx.GetContext(), taskCtx.GetStore(), targetWf.ID, workflowName, params)
	if err != nil {
		return nil, err
	}
	targetWf.Definition = targetVersion.Definition

	// Override defaults with any provided inputs so the started workflow sees them.
	providedInputs, ok := params["inputs"].(map[string]any)
	if !ok || providedInputs == nil {
		providedInputs = make(map[string]any)
	}
	for i, inputDef := range targetWf.Definition.Inputs {
		if val, ok := providedInputs[inputDef.ID]; ok {
			targetWf.Definition.Inputs[i].Default = val
		}
	}
	targetWf.AccountID = accountID
	targetWf.TenantID = tenantID

	// Apply the workflow's own timeout if one is configured; otherwise inherit
	// Temporal's defaults.
	workflowTimeout := 1 * time.Hour
	if targetWf.Definition.Timeout != "" {
		if d, perr := time.ParseDuration(targetWf.Definition.Timeout); perr == nil {
			workflowTimeout = d
		}
	}

	// Keep the Workflow ID short. Including both the parent task ID (a UUID) and a
	// fresh UUID can blow past Temporal's identifier limits and is unnecessary —
	// a `cw-<uuid>` prefix is unique per run and stays well within bounds. Parent
	// linkage is preserved via the SearchAttributes below.
	runWfID := fmt.Sprintf("cw-%s", uuid.New().String())
	// Link the run to the resolved version (version banner + retryability —
	// this fallback starts a top-level workflow) and carry the recursion-depth
	// counter forward.
	memo := model.WorkflowVersionMemo(targetVersion)
	memo[types.MemoKeyCallWorkflowDepth] = callWfDepth + 1

	options := client.StartWorkflowOptions{
		ID:                       runWfID,
		TaskQueue:                config.Config.RunbookServerTemporalQueue,
		WorkflowExecutionTimeout: workflowTimeout,
		SearchAttributes: map[string]any{
			model.SearchAttrTenantID:         tenantID,
			model.SearchAttrAccountID:        accountID,
			model.SearchAttrWorkflowID:       targetWf.ID,
			model.SearchAttrParentWorkflowID: taskCtx.GetWorkflowID(),
		},
		Memo: memo,
	}

	// Use the registered workflow type name as a string (importing the workflow
	// package here would create an import cycle: workflow → tasks → workflow).
	run, err := temporalClient.ExecuteWorkflow(taskCtx.GetContext(), options, "ExecuteWorkflowInternal", targetWf, providedInputs)
	if err != nil {
		return nil, fmt.Errorf("failed to start workflow '%s': %w", workflowName, err)
	}

	// Block on completion. ExecuteWorkflowInternal returns a string; if it is JSON,
	// decode it so the output panel renders the structured value instead of an
	// escaped blob.
	var rawResult string
	if err := run.Get(taskCtx.GetContext(), &rawResult); err != nil {
		return nil, fmt.Errorf("workflow '%s' execution failed: %w", workflowName, err)
	}
	var decoded any = rawResult
	if rawResult != "" {
		var asJSON any
		if jerr := json.Unmarshal([]byte(rawResult), &asJSON); jerr == nil {
			decoded = asJSON
		}
	}

	// `workflow_id` is the called workflow's stored definition ID, not the one-off
	// Temporal Workflow ID we just used to spawn the run (`cw-<uuid>`). Reason:
	// service.go's drill-down post-pass calls GetDetailedWorkflowExecution which
	// resolves via ResolveTemporalWorkflowID, querying Temporal Visibility on
	// `SearchAttrWorkflowID = <id>` — and we set SearchAttrWorkflowID to
	// `targetWf.ID` below. Passing the throwaway `cw-…` id here would fail that
	// resolve and break the executions panel drill-down. The definition ID is
	// also more useful to downstream templates ("which workflow was called") than
	// a per-run id with no independent meaning.
	return map[string]any{
		"workflow_id": targetWf.ID,
		"run_id":      run.GetRunID(),
		"output":      decoded,
	}, nil
}

func (t *CallWorkflowTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"workflow_name": {
				Type:        "string",
				Description: "The name of the workflow to execute.",
				Required:    true,
			},
			"inputs": {
				Type:        "object",
				Description: "Inputs to pass to the called workflow.",
				Required:    false,
			},
			"workflow_version": {
				Type:        "integer",
				Description: "Pin the call to a specific version number of the target workflow. Omit (or 0) to always run the callee's current Live version.",
				Required:    false,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *CallWorkflowTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"workflow_id": {
				Type:        "string",
				Required:    true,
				Description: "The ID of the executed child workflow.",
			},
			"run_id": {
				Type:        "string",
				Required:    true,
				Description: "The Run ID of the executed child workflow.",
			},
			"output": {
				Type:        "object",
				Description: "The final output of the executed child workflow.",
				Required:    true,
			},
		},
	}
}

// GetChildWorkflowDefinition fetches the definition of the referenced workflow and prepares it for execution.
func (t *CallWorkflowTask) GetChildWorkflowDefinition(taskCtx types.TaskContext, params map[string]any) (*model.WorkflowDefinition, error) {
	workflowName, ok := params["workflow_name"].(string)
	if !ok || workflowName == "" {
		return nil, fmt.Errorf("missing or invalid 'workflow_name' parameter for core.call-workflow task")
	}

	// Retrieve tenantID and accountID from the TaskContext
	tenantID := taskCtx.GetTenantID()   // Fixed typo
	accountID := taskCtx.GetAccountID() // Fixed typo

	if tenantID == "" || accountID == "" {
		return nil, fmt.Errorf("tenantID or accountID missing from TaskContext for core.call-workflow task")
	}

	// Fetch the workflow row, then resolve the version to run: the pinned version
	// when the config specifies one (`workflow_version`), else the callee's LIVE
	// published version — never its draft (workflows.definition), otherwise a
	// published parent silently runs the callee's unpublished edits (H2, #282).
	wf, err := taskCtx.GetStore().FindByName(taskCtx.GetContext(), tenantID, accountID, workflowName)
	if err != nil {
		return nil, fmt.Errorf("failed to find workflow '%s' referenced by core.call-workflow task: %w", workflowName, err)
	}
	if wf == nil {
		return nil, fmt.Errorf("workflow '%s' referenced by core.call-workflow task not found", workflowName)
	}
	targetVersion, err := resolveTargetVersion(taskCtx.GetContext(), taskCtx.GetStore(), wf.ID, workflowName, params)
	if err != nil {
		return nil, err
	}
	liveDef := targetVersion.Definition

	// Apply provided inputs to the child workflow's definition
	providedInputs, _ := params["inputs"].(map[string]any) // Can be nil if not provided

	// Create a new slice for inputs to avoid modifying the original workflow definition fetched from store
	childInputs := make([]model.Input, len(liveDef.Inputs))
	copy(childInputs, liveDef.Inputs)

	for i, inputDef := range childInputs {
		if val, ok := providedInputs[inputDef.ID]; ok {
			childInputs[i].Default = val // Override default with provided input
		}
	}

	// Create a new WorkflowDefinition to return, ensuring we don't modify the stored object directly.
	childWfDef := &model.WorkflowDefinition{
		Version:          liveDef.Version,
		Inputs:           childInputs,
		Triggers:         nil, // Child workflows don't use triggers defined in their definition
		Tasks:            liveDef.Tasks,
		Hooks:            liveDef.Hooks,
		Output:           liveDef.Output,
		SetExecutionTags: liveDef.SetExecutionTags,
		RetryPolicy:      liveDef.RetryPolicy,
		Timeout:          liveDef.Timeout,
	}

	return childWfDef, nil
}

// ShouldExecuteAsChildWorkflow indicates that this task should be executed as a proper Child Workflow.
func (t *CallWorkflowTask) ShouldExecuteAsChildWorkflow() bool {
	return true
}
