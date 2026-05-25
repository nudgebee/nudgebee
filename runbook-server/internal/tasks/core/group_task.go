package core

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"
)

// GroupTask implements the Task interface for executing a sub-workflow.
type GroupTask struct{}

var _ types.TaskInlineWorkflow = (*GroupTask)(nil) // Verify interface compliance

func (t *GroupTask) GetName() string {
	return "core.group"
}

// GetDescription returns a brief description of the task.
func (t *GroupTask) GetDescription() string {
	return "Run a group of tasks together as a single step."
}

// GetDisplayName returns a human-readable name for the task.
func (t *GroupTask) GetDisplayName() string {
	return "Group"
}

func (t *GroupTask) Execute(taskCtx types.TaskContext, params map[string]any) (result any, err error) {
	// The execution logic for group tasks has been moved to the workflow executor (internal/workflow/executor.go)
	// to avoid persisting ephemeral workflow definitions in the database.
	// This method should strictly not be called in the new architecture.
	taskCtx.GetLogger().Warn("GroupTask.Execute called - this is unexpected in the new architecture as group tasks should be handled inline by the executor.")
	return nil, fmt.Errorf("GroupTask.Execute is deprecated and should not be called directly")
}

func (t *GroupTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"tasks": {
				Type:        "array",
				Description: "The list of tasks to execute in the group.",
				Required:    true,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *GroupTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"workflowId": {
				Type:        "string",
				Description: "Child Workflow Id",
				Required:    true,
			},
			"runId": {
				Type:        "string",
				Description: "Child Workflow Run Id",
				Required:    true,
			},
		},
	}
}

// GetChildWorkflowDefinition constructs a dynamic workflow definition based on the provided parameters.
// This allows the executor to run the group task as an inline child workflow.
func (t *GroupTask) GetChildWorkflowDefinition(taskCtx types.TaskContext, params map[string]any) (*model.WorkflowDefinition, error) {
	tasksData, ok := params["tasks"]
	if !ok {
		return nil, fmt.Errorf("missing 'tasks' parameter for group task")
	}

	var childTasks []model.Task

	// Handle if it's already []model.Task (passed from struct field injection)
	if tasks, ok := tasksData.([]model.Task); ok {
		childTasks = tasks
	} else if tasksSlice, ok := tasksData.([]any); ok {
		// Handle []any (from Params map)
		tasksBytes, err := json.Marshal(tasksSlice)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal group tasks: %w", err)
		}
		if err := json.Unmarshal(tasksBytes, &childTasks); err != nil {
			return nil, fmt.Errorf("failed to unmarshal group tasks: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid type for 'tasks' parameter: %T", tasksData)
	}

	return &model.WorkflowDefinition{
		Tasks:  childTasks,
		Inputs: make([]model.Input, 0),
	}, nil
}
