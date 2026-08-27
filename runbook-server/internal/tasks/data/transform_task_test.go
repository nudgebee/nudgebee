package data

import (
	"encoding/json"
	"nudgebee/runbook/internal/tasks/types" // Import needed for types.TaskContext
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Dummy variable to explicitly use the 'types' package and avoid unused import error.
var _ types.TaskContext

func TestTransformTask_Execute(t *testing.T) {
	task := &TransformTask{}
	ctx := GetTestTaskContext() // Use helper function

	testCases := []struct {
		name          string
		params        map[string]any
		expected      any
		expectErr     bool
		expectedError string
	}{
		// ... test cases
		{
			name: "Simple JSON to Raw Object (JSONata default)",
			params: map[string]any{
				"input":      `{"name": "Alice", "role": "admin"}`,
				"expression": "name",
			},
			expected: "Alice",
		},
		{
			name: "Simple YAML to Raw Object (JSONata default)",
			params: map[string]any{
				"inputType":  "yaml",
				"input":      "name: Bob\nrole: user",
				"expression": "name",
			},
			expected: "Bob",
		},
		{
			name: "JSON to JSON String (JSONata)",
			params: map[string]any{
				"input":      `{"users": [{"name": "Alice"}, {"name": "Bob"}]}`,
				"expression": "users.name",
				"outputType": "json",
			},
			expected: `["Alice","Bob"]`,
		},
		{
			name: "JSON to YAML String (JSONata)",
			params: map[string]any{
				"input":      `{"users": [{"name": "Alice"}, {"name": "Bob"}]}`,
				"expression": "users",
				"outputType": "yaml",
			},
			expected: "- name: Alice\n- name: Bob\n",
		},
		{
			name: "JSON to Plain String (JSONata)",
			params: map[string]any{
				"input":      `{"count": 42}`,
				"expression": "count",
				"outputType": "string",
			},
			expected: "42",
		},
		{
			name: "JavaScript: Simple Transformation",
			params: map[string]any{
				"scriptType": "javascript",
				"input":      `{"a": 1, "b": 2}`,
				"expression": `data.c = data.a + data.b; result = data;`,
			},
			expected: map[string]any{"a": float64(1), "b": float64(2), "c": float64(3)},
		},
		{
			name: "JavaScript: Conditional Logic",
			params: map[string]any{
				"scriptType": "javascript",
				"input":      `{"value": 10}`,
				"expression": `if (data.value > 5) { result = "high"; } else { result = "low"; }`,
			},
			expected: "high",
		},
		{
			name: "JavaScript: Return Value",
			params: map[string]any{
				"scriptType": "javascript",
				"input":      `{"name": "Test"}`,
				"expression": `result = "Hello, " + data.name;`,
			},
			expected: "Hello, Test",
		},
		{
			name: "JavaScript: JSON Output",
			params: map[string]any{
				"scriptType": "javascript",
				"input":      `{"items": [{"id": 1}]}`,
				"expression": `data.items[0].status = "processed"; result = data;`,
				"outputType": "json",
			},
			expected: `{"items":[{"id":1,"status":"processed"}]}`,
		},
		{
			name: "JavaScript: Invalid Syntax",
			params: map[string]any{
				"scriptType": "javascript",
				"input":      `{"a": 1}`,
				"expression": `data.a = ;`, // Syntax error
			},
			expectErr:     true,
			expectedError: "failed to execute JavaScript expression",
		},
		{
			name: "Missing Expression",
			params: map[string]any{
				"input": `{"name": "test"}`,
			},
			expectErr:     true,
			expectedError: "missing required parameter: 'expression'",
		},
		{
			name: "Invalid Input JSON",
			params: map[string]any{
				"input":      `{"name": "test"`,
				"expression": "name",
			},
			expectErr:     true,
			expectedError: "failed to parse JSON input",
		},
		{
			name: "Invalid Input YAML",
			params: map[string]any{
				"inputType":  "yaml",
				"input":      "name: - Bob",
				"expression": "name",
			},
			expectErr:     true,
			expectedError: "failed to parse YAML input",
		},
		{
			name: "Invalid JSONata Expression",
			params: map[string]any{
				"input":      `{"name": "test"}`,
				"expression": "name[",
			},
			expectErr:     true,
			expectedError: "failed to compile JSONata expression",
		},
		{
			name: "Unsupported Input Type",
			params: map[string]any{
				"inputType":  "xml",
				"input":      "<xml></xml>",
				"expression": "name",
			},
			expectErr:     true,
			expectedError: "unsupported inputType: 'xml'",
		},
		{
			name: "Unsupported Output Type",
			params: map[string]any{
				"input":      `{"name": "test"}`,
				"expression": "name",
				"outputType": "xml",
			},
			expectErr:     true,
			expectedError: "unsupported outputType: 'xml'",
		},
		{
			name: "Unsupported Script Type",
			params: map[string]any{
				"scriptType": "python",
				"input":      `{"a": 1}`,
				"expression": `data.a + 1`,
			},
			expectErr:     true,
			expectedError: "unsupported scriptType: 'python'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := task.Execute(ctx, tc.params)

			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
				resMap, ok := result.(map[string]any)
				assert.True(t, ok) // Ensure result is a map
				actualData := resMap["data"]

				// JSON unmarshal for comparison if expected is map[string]any due to float64 conversion by Goja
				if expectedMap, ok := tc.expected.(map[string]any); ok {
					actualMap, _ := actualData.(map[string]any)
					expectedJSON, _ := json.Marshal(expectedMap)
					actualJSON, _ := json.Marshal(actualMap)
					assert.JSONEq(t, string(expectedJSON), string(actualJSON))
				} else {
					assert.Equal(t, tc.expected, actualData)
				}
			}
		})
	}
}

// Same contract as TestFilterTask_SchemaExamplesAreRunnable: a click-to-fill
// example that errors is worse than no example. Each expression example names
// its engine in the first word of its Note ("JSONata — …" / "JavaScript — …"),
// which is what decides the scriptType it is executed under here.
func TestTransformTask_SchemaExamplesAreRunnable(t *testing.T) {
	ctx := GetTestTaskContext()
	task := &TransformTask{}
	props := task.InputSchema().Properties

	inputExamples := props["input"].Examples
	expressionExamples := props["expression"].Examples
	assert.NotEmpty(t, inputExamples, "input must offer examples")
	assert.NotEmpty(t, expressionExamples, "expression must offer examples")
	assert.NotEmpty(t, props["input"].Help, "input must offer help")
	assert.NotEmpty(t, props["expression"].Help, "expression must offer help")

	literalInputs := make([]string, 0, len(inputExamples))
	for _, example := range inputExamples {
		value, ok := example.Value.(string)
		assert.True(t, ok, "input example %q must be a string", example.Label)
		if strings.Contains(value, "{{") {
			continue
		}
		var parsed any
		assert.NoError(t, json.Unmarshal([]byte(value), &parsed), "input example %q must parse as JSON", example.Label)
		literalInputs = append(literalInputs, value)
	}
	assert.NotEmpty(t, literalInputs, "at least one input example must be a literal document")

	for _, expression := range expressionExamples {
		value, ok := expression.Value.(string)
		assert.True(t, ok, "expression example %q must be a string", expression.Label)

		scriptType := "jsonata"
		if strings.HasPrefix(expression.Note, "JavaScript") {
			scriptType = "javascript"
		}

		for _, input := range literalInputs {
			_, err := task.Execute(ctx, map[string]any{
				"input":      input,
				"inputType":  "json",
				"scriptType": scriptType,
				"expression": value,
			})
			assert.NoErrorf(t, err, "expression example %q (%s) failed against input %s", expression.Label, scriptType, input)
		}
	}
}
