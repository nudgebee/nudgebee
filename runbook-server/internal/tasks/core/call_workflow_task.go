package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model" // Updated import to model
	"nudgebee/runbook/internal/tasks/types"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
)

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

	// Look up the target workflow definition.
	targetWf, err := taskCtx.GetStore().FindByName(taskCtx.GetContext(), tenantID, accountID, workflowName)
	if err != nil {
		return nil, fmt.Errorf("failed to find workflow '%s': %w", workflowName, err)
	}

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
		Memo: map[string]any{
			types.MemoKeyCallWorkflowDepth: callWfDepth + 1,
		},
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

	// Fetch the workflow definition from the store
	wf, err := taskCtx.GetStore().FindByName(context.TODO(), tenantID, accountID, workflowName)
	if err != nil {
		return nil, fmt.Errorf("failed to find workflow '%s' referenced by core.call-workflow task: %w", workflowName, err)
	}

	// Apply provided inputs to the child workflow's definition
	providedInputs, _ := params["inputs"].(map[string]any) // Can be nil if not provided

	// Create a new slice for inputs to avoid modifying the original workflow definition fetched from store
	childInputs := make([]model.Input, len(wf.Definition.Inputs))
	copy(childInputs, wf.Definition.Inputs)

	for i, inputDef := range childInputs {
		if val, ok := providedInputs[inputDef.ID]; ok {
			childInputs[i].Default = val // Override default with provided input
		}
	}

	// Create a new WorkflowDefinition to return, ensuring we don't modify the stored object directly.
	childWfDef := &model.WorkflowDefinition{
		Version:          wf.Definition.Version,
		Inputs:           childInputs,
		Triggers:         nil, // Child workflows don't use triggers defined in their definition
		Tasks:            wf.Definition.Tasks,
		Hooks:            wf.Definition.Hooks,
		Output:           wf.Definition.Output,
		SetExecutionTags: wf.Definition.SetExecutionTags,
		RetryPolicy:      wf.Definition.RetryPolicy,
		Timeout:          wf.Definition.Timeout,
	}

	return childWfDef, nil
}

// ShouldExecuteAsChildWorkflow indicates that this task should be executed as a proper Child Workflow.
func (t *CallWorkflowTask) ShouldExecuteAsChildWorkflow() bool {
	return true
}
