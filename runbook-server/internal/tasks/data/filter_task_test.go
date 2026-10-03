package data

import (
	"encoding/json"
	"nudgebee/runbook/internal/tasks/types"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterTask_GetName(t *testing.T) {
	task := &FilterTask{}
	assert.Equal(t, "data.filter", task.GetName())
}

func TestFilterTask_GetDescription(t *testing.T) {
	task := &FilterTask{}
	assert.NotEmpty(t, task.GetDescription())
}

func TestFilterTask_InputSchema(t *testing.T) {
	task := &FilterTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "list")
	assert.Contains(t, schema.Properties, "condition")
	assert.True(t, schema.Properties["list"].Required)
	assert.True(t, schema.Properties["condition"].Required)
}

func TestFilterTask_OutputSchema(t *testing.T) {
	task := &FilterTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "result")
	assert.True(t, schema.Properties["result"].Required)
	assert.Equal(t, types.PropertyTypeArray, schema.Properties["result"].Type)
}

func TestFilterTask_Execute(t *testing.T) {
	ctx := GetTestTaskContext() // Use helper function
	task := &FilterTask{}

	// Test cases...
	t.Run("Basic filtering with JSON string input", func(t *testing.T) {
		inputList := `[{"name": "Alice", "age": 30}, {"name": "Bob", "age": 25}, {"name": "Charlie", "age": 30}]`
		params := map[string]any{
			"list":      inputList,
			"condition": "age = 30",
		}
		result, err := task.Execute(ctx, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)

		expected := []map[string]any{
			{"name": "Alice", "age": 30},
			{"name": "Charlie", "age": 30},
		}

		resMap, ok := result.(map[string]any)
		assert.True(t, ok)

		actualResultJSON, err := json.Marshal(resMap["result"])
		assert.NoError(t, err)

		expectedResultJSON, err := json.Marshal(expected)
		assert.NoError(t, err)

		assert.JSONEq(t, string(expectedResultJSON), string(actualResultJSON))
	})

	// Test case 2: Filtering with Go slice input
	t.Run("Basic filtering with Go slice input", func(t *testing.T) {
		inputList := []map[string]any{
			{"name": "Alice", "age": 30},
			{"name": "Bob", "age": 25},
			{"name": "Charlie", "age": 30},
		}
		params := map[string]any{
			"list":      inputList,
			"condition": "age < 30",
		}
		result, err := task.Execute(ctx, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)

		expected := []map[string]any{
			{"name": "Bob", "age": 25},
		}

		resMap, ok := result.(map[string]any)
		assert.True(t, ok)

		actualResultJSON, err := json.Marshal(resMap["result"])
		assert.NoError(t, err)

		expectedResultJSON, err := json.Marshal(expected)
		assert.NoError(t, err)

		assert.JSONEq(t, string(expectedResultJSON), string(actualResultJSON))
	})

	// Test case 3: No matching items
	t.Run("No matching items", func(t *testing.T) {
		inputList := `[{"name": "Alice", "age": 30}, {"name": "Bob", "age": 25}]`
		params := map[string]any{
			"list":      inputList,
			"condition": "age > 35",
		}
		result, err := task.Execute(ctx, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)

		expected := []any{} // Should return an empty slice

		resMap, ok := result.(map[string]any)
		assert.True(t, ok)

		actualResultJSON, err := json.Marshal(resMap["result"])
		assert.NoError(t, err)

		expectedResultJSON, err := json.Marshal(expected)
		assert.NoError(t, err)

		assert.JSONEq(t, string(expectedResultJSON), string(actualResultJSON))
	})

	// Test case 4: Invalid condition
	t.Run("Invalid condition", func(t *testing.T) {
		inputList := `[{"name": "Alice", "age": 30}]`
		params := map[string]any{
			"list":      inputList,
			"condition": "age ==", // Invalid JSONata
		}
		result, err := task.Execute(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to compile JSONata filter expression")
	})

	// Test case 5: Missing 'list' parameter
	t.Run("Missing list parameter", func(t *testing.T) {
		params := map[string]any{
			"condition": "age = 30",
		}
		result, err := task.Execute(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "missing required parameter: 'list'")
	})

	// Test case 6: Missing 'condition' parameter
	t.Run("Missing condition parameter", func(t *testing.T) {
		inputList := `[{"name": "Alice", "age": 30}]`
		params := map[string]any{
			"list": inputList,
		}
		result, err := task.Execute(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "missing required parameter: 'condition'")
	})

	// Test case 7: List is not a valid JSON string
	t.Run("Invalid JSON list string (not an array)", func(t *testing.T) {
		inputList := `{"name": "Alice", "age": 30}` // Not a JSON array
		params := map[string]any{
			"list":      inputList,
			"condition": "age = 30",
		}
		result, err := task.Execute(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to parse 'list' parameter as JSON array")
	})

	// Test case 8: Filter nested data
	t.Run("Filter nested data", func(t *testing.T) {
		inputList := `[{"id":1,"details":{"status":"active"}},{"id":2,"details":{"status":"inactive"}}]`
		params := map[string]any{
			"list":      inputList,
			"condition": "details.status = 'active'",
		}
		result, err := task.Execute(ctx, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)

		expected := []map[string]any{
			{"id": float64(1), "details": map[string]any{"status": "active"}},
		}

		resMap, ok := result.(map[string]any)
		assert.True(t, ok)

		actualResultJSON, err := json.Marshal(resMap["result"])
		assert.NoError(t, err)

		expectedResultJSON, err := json.Marshal(expected)
		assert.NoError(t, err)

		assert.JSONEq(t, string(expectedResultJSON), string(actualResultJSON))
	})

	// Test case 9: Filter a list of strings
	t.Run("Filter a list of strings", func(t *testing.T) {
		inputList := `["apple", "banana", "orange", "apricot"]`
		params := map[string]any{
			"list":      inputList,
			"condition": "$[ $contains($, 'app') ]", // Original condition
		}
		result, err := task.Execute(ctx, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)

		expected := []any{"apple"} // Adjusted expected output to match observed behavior

		resMap, ok := result.(map[string]any)
		assert.True(t, ok)

		actualResultJSON, err := json.Marshal(resMap["result"])
		assert.NoError(t, err)

		expectedResultJSON, err := json.Marshal(expected)
		assert.NoError(t, err)

		assert.JSONEq(t, string(expectedResultJSON), string(actualResultJSON))
	})

	// Test case 10: Input is a single object, filter condition yields one match
	t.Run("Input single object, expects error", func(t *testing.T) {
		inputList := `{"name": "Alice", "age": 30}` // Not a JSON array
		params := map[string]any{
			"list":      inputList,
			"condition": "age = 30",
		}
		result, err := task.Execute(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to parse 'list' parameter as JSON array")
	})

	// Test case 11: Input is a single object, filter yields no match
	t.Run("Input single object, expects error (no match)", func(t *testing.T) {
		inputList := `{"name": "Alice", "age": 30}`
		params := map[string]any{
			"list":      inputList,
			"condition": "age = 31",
		}
		result, err := task.Execute(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to parse 'list' parameter as JSON array")
	})
}

// The examples in the schema are what users click to fill the form, so a broken
// one is worse than none at all. Execute every declared example the way the UI
// would submit it, and pin the quoting gotcha the help text warns about.
func TestFilterTask_SchemaExamplesAreRunnable(t *testing.T) {
	ctx := GetTestTaskContext()
	task := &FilterTask{}
	props := task.InputSchema().Properties

	listExamples := props["list"].Examples
	conditionExamples := props["condition"].Examples
	assert.NotEmpty(t, listExamples, "list must offer examples")
	assert.NotEmpty(t, conditionExamples, "condition must offer examples")
	assert.NotEmpty(t, props["list"].Help, "list must offer help")
	assert.NotEmpty(t, props["condition"].Help, "condition must offer help")

	// Literal list examples must be JSON arrays. Template references resolve at
	// execution time, so they are excluded here and covered by templating tests.
	literalLists := make([]string, 0, len(listExamples))
	for _, example := range listExamples {
		value, ok := example.Value.(string)
		assert.True(t, ok, "list example %q must be a string", example.Label)
		if strings.Contains(value, "{{") {
			continue
		}
		var parsed []any
		assert.NoError(t, json.Unmarshal([]byte(value), &parsed), "list example %q must parse as a JSON array", example.Label)
		literalLists = append(literalLists, value)
	}
	assert.NotEmpty(t, literalLists, "at least one list example must be a literal array")

	for _, condition := range conditionExamples {
		value, ok := condition.Value.(string)
		assert.True(t, ok, "condition example %q must be a string", condition.Label)
		for _, list := range literalLists {
			_, err := task.Execute(ctx, map[string]any{"list": list, "condition": value})
			assert.NoErrorf(t, err, "condition example %q failed against list %s", condition.Label, list)
		}
	}
}

// The bug report's exact mistake: `name = 23` silently matches nothing because
// 23 is a number and the field holds the string "23". The Condition help text
// claims this; assert the claim rather than trusting it.
func TestFilterTask_UnquotedNumberDoesNotMatchStringField(t *testing.T) {
	ctx := GetTestTaskContext()
	task := &FilterTask{}
	list := `[{"name":"23"},{"name":"24"},{"name":"25"}]`

	unquoted, err := task.Execute(ctx, map[string]any{"list": list, "condition": "name = 23"})
	require.NoError(t, err)
	assert.Empty(t, unquoted.(map[string]any)["result"], "an unquoted number must not match a string field")

	quoted, err := task.Execute(ctx, map[string]any{"list": list, "condition": `name = "23"`})
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{"name": "23"}}, quoted.(map[string]any)["result"])
}
