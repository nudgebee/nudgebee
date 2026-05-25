package data

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"

	"github.com/dop251/goja" // Added Goja import
	jsonata "github.com/xiatechs/jsonata-go"
	"gopkg.in/yaml.v3"
)

// TransformTask implements the Task interface for data transformation using JSONata or JavaScript.
type TransformTask struct{}

func (t *TransformTask) GetName() string {
	return "data.transform"
}

// GetDescription returns a brief description of the task.
func (t *TransformTask) GetDescription() string {
	return "Reshape or transform data using expressions or scripts."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TransformTask) GetDisplayName() string {
	return "Data Transform"
}

func (t *TransformTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	// 1. Get parameters
	expression, _ := params["expression"].(string)
	inputType, _ := params["inputType"].(string)
	outputType, _ := params["outputType"].(string)
	scriptType, _ := params["scriptType"].(string) // Get scriptType

	if expression == "" {
		return nil, fmt.Errorf("missing required parameter: 'expression'")
	}

	// Default scriptType
	if scriptType == "" {
		scriptType = "jsonata"
	}

	// 2. Parse input string into a Go data structure
	rawInput := params["input"]
	var data any
	var inputStr string

	if str, ok := rawInput.(string); ok {
		inputStr = str
	} else if rawInput != nil {
		data = rawInput
	}

	if data == nil {
		if inputType == "" {
			inputType = "json" // Default to json
		}

		switch inputType {
		case "yaml":
			if err := yaml.Unmarshal([]byte(inputStr), &data); err != nil {
				taskCtx.GetLogger().Error("unable to process yaml", "data", inputStr, "error", err)
				return nil, fmt.Errorf("failed to parse YAML input: %w", err)
			}
		case "json":
			if err := json.Unmarshal([]byte(inputStr), &data); err != nil {
				taskCtx.GetLogger().Error("unable to process json", "data", inputStr, "error", err)
				return nil, fmt.Errorf("failed to parse JSON input: %w", err)
			}
		default:
			return nil, fmt.Errorf("unsupported inputType: '%s'", inputType)
		}
	}

	var result any
	var err error

	switch scriptType {
	case "jsonata":
		// 3. Execute JSONata expression
		expr, jsonataErr := jsonata.Compile(expression)
		if jsonataErr != nil {
			return nil, fmt.Errorf("failed to compile JSONata expression: %w", jsonataErr)
		}

		result, jsonataErr = expr.Eval(data)
		if jsonataErr != nil {
			if jsonataErr.Error() == "no results found" {
				result = nil
			} else {
				return nil, fmt.Errorf("failed to evaluate JSONata expression: %w", jsonataErr)
			}
		}
	case "javascript":
		// 3. Execute JavaScript expression using Goja
		vm := goja.New()

		// Make input data available to the JavaScript context
		err = vm.Set("data", data)
		if err != nil {
			return nil, fmt.Errorf("failed to set input data in JavaScript VM: %w", err)
		}

		// Execute the JavaScript code
		jsResult, jsErr := vm.RunString(expression)
		if jsErr != nil {
			return nil, fmt.Errorf("failed to execute JavaScript expression: %w", jsErr)
		}

		// Extract result from JavaScript VM
		// If the script returns a value, use it. Otherwise, try to get a 'result' global.
		if jsResult != nil {
			result = jsResult.Export()
		} else if vmResult := vm.Get("result"); vmResult != nil {
			result = vmResult.Export()
		} else {
			result = nil // No explicit result or return value
		}

	default:
		return nil, fmt.Errorf("unsupported scriptType: '%s'", scriptType)
	}

	// 4. Format output based on outputType
	if outputType == "" {
		outputType = "raw" // Default to raw
	}

	switch outputType {
	case "json":
		jsonResult, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal result to JSON: %w", err)
		}
		return map[string]any{
			"data": string(jsonResult),
		}, nil
	case "yaml":
		yamlResult, err := yaml.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal result to YAML: %w", err)
		}
		return map[string]any{
			"data": string(yamlResult),
		}, nil
	case "string":
		if result == nil {
			return map[string]any{
				"data": "",
			}, nil
		}
		return map[string]any{
			"data": fmt.Sprintf("%v", result),
		}, nil
	case "raw":
		return map[string]any{
			"data": result,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported outputType: '%s'", outputType)
	}
}

func (t *TransformTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"input": {
				Type:        types.PropertyTypeString,
				Description: "The raw string data to be transformed (e.g., from a previous task's output).",
				Required:    true,
				Order:       1,
			},
			"inputType": {
				Type:        types.PropertyTypeString,
				Description: "The format of the input data. Can be 'json' or 'yaml'. Defaults to 'json'.",
				Required:    false,
				Options:     []string{"json", "yaml"},
				Default:     "json",
				Order:       2,
			},
			"scriptType": {
				Type:        types.PropertyTypeString,
				Description: "The transformation engine to use. Can be 'jsonata' or 'javascript'. Defaults to 'jsonata'.",
				Required:    false,
				Options:     []string{"jsonata", "javascript"},
				Default:     "jsonata",
				Order:       3,
			},
			"expression": {
				Type:        types.PropertyTypeString,
				Description: "The expression (JSONata or JavaScript) to apply to the input data.",
				Required:    true,
				Order:       4,
			},
			"outputType": {
				Type:        types.PropertyTypeString,
				Description: "The desired format of the output. Can be 'raw', 'json', 'yaml'. Defaults to 'raw'.",
				Required:    false,
				Options:     []string{"raw", "yaml", "json"},
				Default:     "raw",
				Order:       5,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *TransformTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        "string",
				Description: "The transformed data, formatted according to outputType.",
				Required:    true,
			},
		},
	}
}
