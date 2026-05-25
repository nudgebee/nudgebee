package tasks

import (
	"context" // Add this import
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// TaskWrapper adapts a Task to the Temporal activity signature.
type TaskWrapper struct {
	Task           types.Task
	TemporalClient client.Client
	Store          model.WorkflowStore
	Converter      converter.DataConverter
}

const (
	// Internal param keys for workflow metadata
	ParamTenantID   = "_tenant_id"
	ParamAccountID  = "_account_id"
	ParamWorkflowID = "_workflow_id"
	ParamUserID     = "_user_id"
	ParamVars             = "_vars" // New: Current state of global vars map
	ParamDryRun           = "_dry_run"
	ParamWorkflowName     = "_workflow_name"
	ParamUserDisplayName  = "_user_display_name"
)

// Execute is the method registered with Temporal. It extracts workflow metadata from params,
// injects them into context, builds TaskContext, and calls the real Task.Execute.
func (tw *TaskWrapper) Execute(ctx context.Context, params map[string]any) (any, error) {
	if !activity.IsActivity(ctx) {
		return nil, fmt.Errorf("TaskWrapper.Execute must be called within a Temporal Activity context")
	}

	// Extract workflow metadata from params if present
	tenantID, _ := params[ParamTenantID].(string)
	accountID, _ := params[ParamAccountID].(string)
	workflowID, _ := params[ParamWorkflowID].(string)
	userID, _ := params[ParamUserID].(string)
	isDryRun, _ := params[ParamDryRun].(bool)
	workflowName, _ := params[ParamWorkflowName].(string)
	userDisplayName, _ := params[ParamUserDisplayName].(string)

	// Build TaskContext (includes logger, metadata, etc.)
	taskCtx := types.NewTemporalTaskContextFromActivity(ctx, tenantID, accountID, workflowID, userID, workflowName, userDisplayName, tw.TemporalClient, tw.Converter, tw.Store, isDryRun)

	// Remove internal metadata keys from params before passing to real task
	delete(params, ParamTenantID)
	delete(params, ParamAccountID)
	delete(params, ParamWorkflowID)
	delete(params, ParamUserID)
	delete(params, ParamVars) // New: Remove ParamVars from params
	delete(params, ParamDryRun)
	delete(params, ParamWorkflowName)
	delete(params, ParamUserDisplayName)

	if schema := tw.Task.InputSchema(); schema != nil {
		if err := schema.Process(params); err != nil {
			return nil, err
		}
	}

	return tw.Task.Execute(taskCtx, params)
}
