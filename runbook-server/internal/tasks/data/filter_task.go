package data

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"

	jsonata "github.com/xiatechs/jsonata-go"
)

// FilterTask implements the Task interface for filtering lists using JSONata.
type FilterTask struct{}

func (t *FilterTask) GetName() string {
	return "data.filter"
}

// GetDescription returns a brief description of the task.
func (t *FilterTask) GetDescription() string {
	return "Keep only the items in a list that match a condition."
}

// GetDisplayName returns a human-readable name for the task.
func (t *FilterTask) GetDisplayName() string {
	return "Data Filter"
}

func (t *FilterTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	// 1. Get parameters
	condition, _ := params["condition"].(string)
	listInput, ok := params["list"]

	if !ok {
		return nil, fmt.Errorf("missing required parameter: 'list'")
	}
	if condition == "" {
		return nil, fmt.Errorf("missing required parameter: 'condition'")
	}

	// 2. Process list input
	var data any
	switch v := listInput.(type) {
	case string:
		// Try to parse as JSON. Must be a JSON array.
		var rawList []any
		if err := json.Unmarshal([]byte(v), &rawList); err != nil {
			return nil, fmt.Errorf("failed to parse 'list' parameter as JSON array: %w", err)
		}
		data = rawList
	case []any:
		data = v
	case []map[string]any: // Handle case where input is already a slice of maps
		data = v
	default:
		// If it's not a string and not a slice/array, we'll try to treat it as a single item
		// which JSONata can sometimes handle, but for filtering we expect a list.
		// For now, let's wrap it as a single-item array.
		data = []any{v}
	}

	// 3. Construct and execute JSONata expression
	fullExpression := fmt.Sprintf("$[%s]", condition)

	expr, err := jsonata.Compile(fullExpression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile JSONata filter expression: %w", err)
	}

	var result any // Declare result here

	jsonataResult, err := expr.Eval(data)
	if err != nil {
		if err.Error() == "no results found" {
			result = []any{}
		} else {
			return nil, fmt.Errorf("failed to evaluate filter expression: %w", err)
		}
	} else {
		// Ensure the result is always an array for consistency with filtering a list.
		// JSONata can return a single item if only one matches.
		if jsonataResult == nil {
			result = []any{}
		} else {
			// Check if jsonataResult is already a slice
			if _, isSlice := jsonataResult.([]any); isSlice {
				result = jsonataResult
			} else {
				// If not a slice, wrap it in a slice
				result = []any{jsonataResult}
			}
		}
	}

	// 4. Return result
	// The result should probably be returned in a structured way.
	// Should we return it as 'result' or 'data'?
	// TransformTask returns 'data'. Let's stick to 'result' as per plan or 'data' for consistency?
	// The user prompt said: Output Schema: result: The filtered list.
	// So I will use 'result'.
	return map[string]any{
		"result": result,
	}, nil
}

func (t *FilterTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"list": {
				Type:        "any",
				Description: "The list to filter. Can be a JSON string or an array.",
				Required:    true,
			},
			"condition": {
				Type:        "string",
				Description: "The condition to apply (JSONata predicate).",
				Required:    true,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *FilterTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"result": {
				Type:        "array",
				Description: "The filtered list.",
				Required:    true,
			},
		},
	}
}
