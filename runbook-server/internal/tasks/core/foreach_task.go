package core

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"
)

type ForEachTask struct{}

var _ types.TaskLoop = (*ForEachTask)(nil)

func (t *ForEachTask) GetName() string {
	return "core.foreach"
}

func (t *ForEachTask) GetDescription() string {
	return "Loop through a list and run tasks for each item. Provide a list of items and a set of tasks — the workflow executes those tasks once per item. Supports parallel execution with configurable concurrency."
}

func (t *ForEachTask) GetDisplayName() string {
	return "For Each"
}

func (t *ForEachTask) Execute(taskCtx types.TaskContext, params map[string]any) (result any, err error) {
	return nil, fmt.Errorf("ForEachTask execution should be handled by the workflow executor")
}

func (t *ForEachTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"items": {
				Type:        "any",
				Description: "List of items to iterate over",
				Required:    true,
			},
			"tasks": {
				Type:        "array",
				Description: "List of tasks to execute for each iteration",
				Required:    true,
			},
			"item": {
				Type:        "string",
				Description: "Variable name for the current item (default: 'item')",
				Default:     "item",
				Required:    false,
			},
			"concurrency": {
				Type:        "integer",
				Description: "Max parallel iterations (0 or less means sequential, 1 means one at a time, >1 means N in parallel).",
				Required:    false,
				Default:     1,
			},
			"output": {
				Type:        "object",
				Description: "Map of outputs to extract from each iteration",
				Required:    false,
			},
		},
	}
}

func (t *ForEachTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"results": {
				Type:        "array",
				Description: "List of outputs from each iteration",
				Required:    true,
			},
		},
	}
}

func (t *ForEachTask) RuntimeNotes() []string {
	return []string{
		"Access current item via {{ item }} (or custom name set via 'item' param) inside loop body tasks.",
		"The item variable is injected into Vars and flattened to root context, so {{ item.field }} works directly.",
		"If 'items' is a JSON string, it will be automatically unmarshalled to an array.",
		"Set concurrency > 1 for parallel execution. Default is sequential (concurrency=1).",
	}
}

func (t *ForEachTask) GetLoopConfig(taskCtx types.TaskContext, params map[string]any) (*types.LoopConfiguration, error) {
	itemsRaw, ok := params["items"]
	if !ok {
		return nil, fmt.Errorf("missing 'items' parameter")
	}

	var items []any
	if itemsSlice, ok := itemsRaw.([]any); ok {
		items = itemsSlice
	} else if itemInterfaceSlice, ok := itemsRaw.([]interface{}); ok { // Handles different internal Go representations of slices
		items = append(items, itemInterfaceSlice...)
	} else if itemsStr, ok := itemsRaw.(string); ok {
		if err := json.Unmarshal([]byte(itemsStr), &items); err != nil {
			return nil, fmt.Errorf("failed to unmarshal 'items' string: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid type for 'items' parameter: expected array or JSON string array, got %T", itemsRaw)
	}

	itemVarAny, ok := params["item"]
	var itemVar string
	if ok {
		if itemVar, ok = itemVarAny.(string); !ok {
			return nil, fmt.Errorf("invalid type for 'item' parameter: expected string, got %T", itemVarAny)
		}
	} else {
		itemVar = "item" // Default value
	}

	concurrency := 1
	if c, ok := params["concurrency"]; ok {
		if cInt, ok := c.(int); ok {
			concurrency = cInt
		} else if cFloat, ok := c.(float64); ok {
			concurrency = int(cFloat)
		}
	}

	tasksData, ok := params["tasks"]
	if !ok {
		return nil, fmt.Errorf("missing 'tasks' parameter")
	}

	var childTasks []model.Task
	if tasks, ok := tasksData.([]model.Task); ok {
		childTasks = tasks
	} else if tasksSlice, ok := tasksData.([]any); ok {
		tasksBytes, err := json.Marshal(tasksSlice)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal tasks: %w", err)
		}
		if err := json.Unmarshal(tasksBytes, &childTasks); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tasks: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid type for 'tasks' parameter: %T", tasksData)
	}

	var iterationOutput map[string]any
	if out, ok := params["output"]; ok {
		if outMap, ok := out.(map[string]any); ok {
			iterationOutput = outMap
		}
	}

	return &types.LoopConfiguration{
		Items:       items,
		ItemVarName: itemVar,
		Body:        &model.WorkflowDefinition{Tasks: childTasks, Output: iterationOutput},
		BatchSize:   concurrency,
	}, nil
}
