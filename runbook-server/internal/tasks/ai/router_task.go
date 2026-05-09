package ai

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"
)

// RouterTask implements the Task interface for executing conditional branches based on AI decision.
type RouterTask struct{}

func (t *RouterTask) GetName() string {
	return "llm.router"
}

// GetDescription returns a brief description of the task.
func (t *RouterTask) GetDescription() string {
	return "Use AI to classify input and route to the right branch of tasks. Define multiple branches with descriptions — the AI reads the prompt and automatically selects which branch to execute based on the input content."
}

// GetDisplayName returns a human-readable name for the task.
func (t *RouterTask) GetDisplayName() string {
	return "AI Router"
}

func (t *RouterTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Warn("RouterTask.Execute called - this is unexpected as router tasks should be handled inline by the executor.")
	return nil, fmt.Errorf("RouterTask.Execute is deprecated and should not be called directly")
}

func (t *RouterTask) InputSchema() *types.Schema {
	branchSchema := types.Schema{
		Properties: map[string]types.Property{
			"name": {
				Type:        types.PropertyTypeString,
				Description: "Unique name of the branch.",
				Required:    true,
				Title:       "Branch Name",
				Order:       1,
			},
			"description": {
				Type:        types.PropertyTypeString,
				Description: "Description for the AI to understand this branch.",
				Required:    true,
				Title:       "Description",
				Order:       2,
				SubType:     "textarea",
			},
			"tasks": {
				Type:        types.PropertyTypeArray,
				Description: "List of tasks to execute if this branch is selected.",
				Required:    true,
				SubType:     "object",
				Title:       "Tasks",
				Order:       3,
			},
		},
	}

	return &types.Schema{
		Properties: map[string]types.Property{
			"prompt": {
				Type:        "string",
				Description: "The user query to route.",
				Required:    true,
				Title:       "Prompt",
				Order:       1,
				SubType:     "textarea",
			},
			"branches": {
				Type:        "array",
				Description: "List of branches with name, description and tasks.",
				Schema:      &branchSchema,
				Title:       "Branches",
				Order:       2,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *RouterTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"selected_branch": {
				Type: "string",
			},
		},
	}
}

// GetChildWorkflowDefinition constructs a dynamic workflow definition.
func (t *RouterTask) GetChildWorkflowDefinition(taskCtx types.TaskContext, params map[string]any) (*model.WorkflowDefinition, error) {
	prompt, ok := params["prompt"].(string)
	if !ok || prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	branchesRaw, ok := params["branches"].([]any)
	if !ok || len(branchesRaw) == 0 {
		return nil, fmt.Errorf("branches are required")
	}

	// Prepare options for the decision task and cases for the switch
	var options []map[string]any
	var switchBranches []map[string]any

	// Helper to ensure tasks are in correct format
	unmarshalTasks := func(input any) ([]model.Task, error) {
		var tasks []model.Task
		if tList, ok := input.([]model.Task); ok {
			tasks = tList
		} else if tSlice, ok := input.([]any); ok {
			tasksBytes, err := json.Marshal(tSlice)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tasks: %w", err)
			}
			if err := json.Unmarshal(tasksBytes, &tasks); err != nil {
				return nil, fmt.Errorf("failed to unmarshal tasks: %w", err)
			}
		}
		return tasks, nil
	}

	for i, b := range branchesRaw {
		branchMap, ok := b.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("branch at index %d is invalid", i)
		}
		name, okName := branchMap["name"].(string)
		desc, okDesc := branchMap["description"].(string)
		tasksData := branchMap["tasks"]

		if !okName || name == "" {
			return nil, fmt.Errorf("branch at index %d is missing a valid name", i)
		}
		if !okDesc || desc == "" {
			return nil, fmt.Errorf("branch at index %d is missing a valid description", i)
		}

		options = append(options, map[string]any{
			"name":        name,
			"description": desc,
		})

		// Verify tasks are valid
		_, err := unmarshalTasks(tasksData)
		if err != nil {
			return nil, fmt.Errorf("invalid tasks in branch %s: %w", name, err)
		}

		// Use expression-based switch format: each case matches by branch name
		switchBranches = append(switchBranches, map[string]any{
			"value": name,
			"tasks": tasksData,
		})
	}

	// Define tasks using expression-based switch format
	tasks := []model.Task{
		{
			ID:   "decision",
			Type: "llm.classify",
			Params: map[string]any{
				"prompt":  prompt,
				"options": options,
			},
			SetVars: map[string]any{
				"router_selected_branch": "{{ Self.output.selected_branch }}",
			},
		},
		{
			ID:   "dispatch",
			Type: "core.switch",
			Params: map[string]any{
				"expression": "{{ router_selected_branch }}",
				"cases":      switchBranches,
			},
			DependsOn: []string{"decision"},
		},
	}

	return &model.WorkflowDefinition{
		Tasks:  tasks,
		Inputs: make([]model.Input, 0),
		Output: map[string]any{
			"selected_branch": "{{ Tasks['decision'].output['selected_branch'] }}",
		},
	}, nil
}
