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

// conditionHelp is the JSONata cheatsheet shown in the Condition field's help
// tooltip. It leads with the splice (`$[...]`) because that is what makes the
// syntax look unfamiliar: users are writing the inside of a predicate, not a
// whole expression.
const conditionHelp = "The condition is spliced into `$[<condition>]` and evaluated against each item in the list, " +
	"so you write only the inside of the predicate.\n\n" +
	"**Comparison:** `=`  `!=`  `<`  `<=`  `>`  `>=`\n\n" +
	"**Combine:** `and`  `or`  `in`\n\n" +
	"**Quote your strings.** `name = 23` compares against the number 23 and will not match the string `\"23\"` — " +
	"write `name = \"23\"` for that.\n\n" +
	"**Nested fields** use dots: `details.status = \"active\"`.\n\n" +
	"**Lists of plain strings or numbers** have no field to name, so refer to the item itself as `$`: `$ = \"beta\"`.\n\n" +
	"**Useful functions:** `$contains(name, \"prod\")`, `$number(x)`, `$string(x)`, `$count($)`.\n\n" +
	"Full reference: https://docs.jsonata.org/predicate"

// listHelp explains the one thing the type name "any" hides: the value has to
// be a JSON array, and an object is the most common wrong answer.
const listHelp = "Must be a JSON **array** — `[...]`, not `{...}`. JSON requires double quotes, " +
	"so `[{\"name\": \"web\"}]` is valid and `[{'name': 'web'}]` is not.\n\n" +
	"To filter the output of an earlier action, reference it instead of pasting a literal: " +
	"`{{ Tasks['previous_task'].output.result }}`."

func (t *FilterTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"list": {
				Type:        "any",
				Description: "The list to filter. Can be a JSON string or an array.",
				Required:    true,
				Order:       1,
				Help:        listHelp,
				Examples: []types.PropertyExample{
					{
						Label: `[{"name": "checkout", "cpu": 91}, ...]`,
						Value: `[{"name": "checkout", "cpu": 91}, {"name": "payments", "cpu": 40}]`,
						Note:  "A list of objects — filter it on any field, e.g. `cpu > 80`.",
					},
					{
						Label: `["alpha", "beta"]`,
						Value: `["alpha", "beta"]`,
						Note:  "A list of plain strings — filter it with `$`, e.g. `$ = \"beta\"`.",
					},
					{
						Label: "Output of a previous action",
						Value: "{{ Tasks['previous_task'].output.result }}",
						Note:  "Replace `previous_task` with the id of the action whose output you want to filter.",
					},
				},
			},
			"condition": {
				Type:        "string",
				Description: "The condition to apply (JSONata predicate).",
				Required:    true,
				Order:       2,
				Help:        conditionHelp,
				Examples: []types.PropertyExample{
					{
						Label: `status = "active"`,
						Value: `status = "active"`,
						Note:  "Keep items whose `status` field equals the string `active`.",
					},
					{
						Label: "cpu > 80",
						Value: "cpu > 80",
						Note:  "Numeric comparison — no quotes around the number.",
					},
					{
						Label: `name = "23"`,
						Value: `name = "23"`,
						Note:  "Quotes matter: `name = 23` looks for the number 23 and will not match the string \"23\".",
					},
					{
						Label: `details.status = "active"`,
						Value: `details.status = "active"`,
						Note:  "Reach into a nested field with a dot.",
					},
					{
						Label: `$contains(name, "prod")`,
						Value: `$contains(name, "prod")`,
						Note:  "Substring match instead of an exact one.",
					},
					{
						Label: `$ = "beta"`,
						Value: `$ = "beta"`,
						Note:  "For a list of plain strings or numbers, `$` is the item itself.",
					},
				},
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
