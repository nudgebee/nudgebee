package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractAndUnmarshalJSON(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	tests := []struct {
		name  string
		input string
		run   func(t *testing.T, input string)
	}{
		{
			name:  "Pure JSON object",
			input: `{"name":"John","age":30}`,
			run: func(t *testing.T, input string) {
				var got TestStruct
				err := ExtractAndUnmarshalJSON([]byte(input), &got)

				assert.NoError(t, err)
				assert.Equal(t, TestStruct{Name: "John", Age: 30}, got)
			},
		},
		{
			name:  "Markdown wrapped JSON object",
			input: "```json\n{\"name\":\"John\",\"age\":30}\n```",
			run: func(t *testing.T, input string) {
				var got TestStruct
				err := ExtractAndUnmarshalJSON([]byte(input), &got)

				assert.NoError(t, err)
				assert.Equal(t, TestStruct{Name: "John", Age: 30}, got)
			},
		},
		{
			name:  "JSON object embedded in text",
			input: `Here is the result: {"name":"Jane","age":30}.`,
			run: func(t *testing.T, input string) {
				var got TestStruct
				err := ExtractAndUnmarshalJSON([]byte(input), &got)
				assert.NoError(t, err)
				assert.Equal(t, TestStruct{Name: "Jane", Age: 30}, got)
			},
		},
		{
			name:  "Array to struct succeeds by finding inner object",
			input: `text before [{"name":"John","age":30}] text after`,
			run: func(t *testing.T, input string) {
				var got []TestStruct
				err := ExtractAndUnmarshalJSON([]byte(input), &got)
				assert.NoError(t, err)
				assert.Equal(t, "John", got[0].Name)
			},
		},
		{
			name:  "Multiple JSON objects, recovers from first malformed block",
			input: `Here is JSON1: {"id": 1, malformed} and JSON2: {"name":"John","age":30} some text`,
			run: func(t *testing.T, input string) {
				var got TestStruct
				err := ExtractAndUnmarshalJSON([]byte(input), &got)
				assert.NoError(t, err)
				assert.Equal(t, "John", got.Name)
			},
		},
		{
			name:  "Nested JSON objects - returns outermost successful",
			input: `Response: {"status": "ok", "data": {"name":"John","age":30}}`,
			run: func(t *testing.T, input string) {
				type Wrapper struct {
					Data TestStruct `json:"data"`
				}
				var got Wrapper
				err := ExtractAndUnmarshalJSON([]byte(input), &got)
				assert.NoError(t, err)
				assert.Equal(t, "John", got.Data.Name)
			},
		},
		{
			name:  "Multiple Markdown blocks, recovers from first malformed",
			input: "```json\n{\"id\":1, malformed}\n```\nSome text\n```json\n{\"name\":\"John\",\"age\":30}\n```",
			run: func(t *testing.T, input string) {
				var got TestStruct
				err := ExtractAndUnmarshalJSON([]byte(input), &got)
				assert.NoError(t, err)
				assert.Equal(t, "John", got.Name)
			},
		},
		{
			name:  "JSON with brackets in strings",
			input: `The object is {"name":"Bracket { } in string","age":30}`,
			run: func(t *testing.T, input string) {
				var got TestStruct
				err := ExtractAndUnmarshalJSON([]byte(input), &got)
				assert.NoError(t, err)
				assert.Equal(t, "Bracket { } in string", got.Name)
			},
		},
		{
			name:  "Recovers from mismatched brackets",
			input: `Some junk { [ } ] then valid {"name":"John","age":30}`,
			run: func(t *testing.T, input string) {
				var got TestStruct
				err := ExtractAndUnmarshalJSON([]byte(input), &got)
				assert.NoError(t, err)
				assert.Equal(t, "John", got.Name)
			},
		},

		{
			name:  "Array target succeeds",
			input: `The list is: [{"name":"John","age":30},{"name":"Jane","age":25}]`,
			run: func(t *testing.T, input string) {
				var got []map[string]any
				err := ExtractAndUnmarshalJSON([]byte(input), &got)

				assert.NoError(t, err)
				assert.Len(t, got, 2)
				assert.Equal(t, "John", got[0]["name"])
				assert.Equal(t, "Jane", got[1]["name"])
			},
		},
		{
			name:  "Evaluator feedback with backtick-quoted JSON and escaped quotes",
			input: `{"correctness":0.3,"relevance":0.4,"completeness":0.2,"helpfulness":0.2,"feedback":"The response does not match the structure provided in the task description: ` + "`" + `{ \"subject_name\": \"...\", \"namespace\": \"...\" }` + "`" + `. This formatting error makes it invalid."}`,
			run: func(t *testing.T, input string) {
				type EvalResult struct {
					Correctness  float64 `json:"correctness"`
					Relevance    float64 `json:"relevance"`
					Completeness float64 `json:"completeness"`
					Helpfulness  float64 `json:"helpfulness"`
					Feedback     string  `json:"feedback"`
				}
				var got EvalResult
				err := ExtractAndUnmarshalJSON([]byte(input), &got)
				assert.NoError(t, err)
				assert.Equal(t, 0.3, got.Correctness)
				assert.Contains(t, got.Feedback, "subject_name")
			},
		},
		{
			name:  "Invalid input",
			input: "this is not json",
			run: func(t *testing.T, input string) {
				var got map[string]any
				err := ExtractAndUnmarshalJSON([]byte(input), &got)

				assert.Error(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, tt.input)
		})
	}
}

func TestUnmarshalJsonAsMap(t *testing.T) {
	t.Run("object passes through", func(t *testing.T) {
		out, err := UnmarshalJsonAsMap([]byte(`{"id":"a","name":"alpha"}`))
		assert.NoError(t, err)
		assert.Equal(t, "a", out["id"])
		assert.Equal(t, "alpha", out["name"])
	})

	t.Run("array of objects preserved under items key", func(t *testing.T) {
		data := `[{"id":"a"},{"id":"b"},{"id":"c"}]`
		out, err := UnmarshalJsonAsMap([]byte(data))
		assert.NoError(t, err)
		items, ok := out["items"].([]any)
		assert.True(t, ok, "items should be []any")
		assert.Len(t, items, 3, "all array elements must be preserved")
	})

	t.Run("empty array produces empty items list", func(t *testing.T) {
		out, err := UnmarshalJsonAsMap([]byte(`[]`))
		assert.NoError(t, err)
		items, ok := out["items"].([]any)
		assert.True(t, ok)
		assert.Len(t, items, 0)
	})

	t.Run("array of primitives preserved", func(t *testing.T) {
		out, err := UnmarshalJsonAsMap([]byte(`["a","b","c"]`))
		assert.NoError(t, err)
		items, ok := out["items"].([]any)
		assert.True(t, ok)
		assert.Len(t, items, 3)
	})

	t.Run("scalar wrapped under value key", func(t *testing.T) {
		out, err := UnmarshalJsonAsMap([]byte(`42`))
		assert.NoError(t, err)
		assert.Contains(t, out, "value")
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		_, err := UnmarshalJsonAsMap([]byte(`not json`))
		assert.Error(t, err)
	})

	t.Run("prod gcp cluster payload preserves all clusters", func(t *testing.T) {
		data := `[{"id":"my-gcp-project-dev","name":"my-gcp-project-dev","type":"container.googleapis.com/Cluster"},{"id":"my-gcp-project-prod","name":"my-gcp-project-prod","type":"container.googleapis.com/Cluster"}]`
		out, err := UnmarshalJsonAsMap([]byte(data))
		assert.NoError(t, err)
		items, ok := out["items"].([]any)
		assert.True(t, ok)
		assert.Len(t, items, 2, "must not silently truncate the array")
	})
}
