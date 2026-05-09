package core

import (
	"context"
	"fmt"
	"nudgebee/runbook/internal/model" // Updated import to model
	"nudgebee/runbook/internal/tasks/types"
)

// CallWorkflowTask implements the Task interface for executing another workflow.
type CallWorkflowTask struct {
}

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

func (t *CallWorkflowTask) Execute(taskCtx types.TaskContext, params map[string]any) (result any, err error) {
	// This method should not be called as CallWorkflowTask is an inline workflow type.
	taskCtx.GetLogger().Warn("CallWorkflowTask.Execute called - this is unexpected as it should be handled inline by the executor.")
	return nil, fmt.Errorf("CallWorkflowTask.Execute is deprecated and should not be called directly")
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
