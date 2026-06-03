package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSchemaProcess(t *testing.T) {
	schema := &Schema{
		Properties: map[string]Property{
			"text": {
				Type: "string",
			},
			"number": {
				Type: "number",
			},
			"timestamp": {
				Type: "timestamp",
			},
		},
	}

	t.Run("should dereference pointers", func(t *testing.T) {
		strVal := "hello"
		numVal := 123.45
		params := map[string]any{
			"text":   &strVal,
			"number": &numVal,
		}

		err := schema.Process(params)
		assert.NoError(t, err)

		// Check types in params
		assert.Equal(t, "hello", params["text"])
		assert.Equal(t, 123.45, params["number"])

		_, okStr := params["text"].(string)
		assert.True(t, okStr, "text should be string")

		_, okNum := params["number"].(float64)
		assert.True(t, okNum, "number should be float64")
	})

	t.Run("should handle non-pointers", func(t *testing.T) {
		params := map[string]any{
			"text":   "world",
			"number": 67.8,
		}
		err := schema.Process(params)
		assert.NoError(t, err)
		assert.Equal(t, "world", params["text"])
		assert.Equal(t, 67.8, params["number"])
	})
}
