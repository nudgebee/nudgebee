package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSchemaValidation(t *testing.T) {
	schema := &Schema{
		Properties: map[string]Property{
			"name": {
				Type:     "string",
				Required: true,
			},
			"count": {
				Type:     "int",
				Required: false,
			},
		},
	}

	t.Run("should pass for valid parameters", func(t *testing.T) {
		params := map[string]any{
			"name":  "test-name",
			"count": 10,
		}
		err := schema.Validate(params)
		assert.NoError(t, err)
	})

	t.Run("should pass if optional parameter is missing", func(t *testing.T) {
		params := map[string]any{
			"name": "test-name",
		}
		err := schema.Validate(params)
		assert.NoError(t, err)
	})

	t.Run("should fail if required parameter is missing", func(t *testing.T) {
		params := map[string]any{
			"count": 10,
		}
		err := schema.Validate(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing required parameter: 'name'")
	})

	t.Run("should fail if parameter has incorrect type", func(t *testing.T) {
		params := map[string]any{
			"name":  123, // Should be string
			"count": 10,
		}
		err := schema.Validate(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid type for parameter 'name'")
	})

	t.Run("should accept template strings for non-string typed params", func(t *testing.T) {
		templateSchema := &Schema{
			Properties: map[string]Property{
				"arguments":  {Type: PropertyTypeObject},
				"headers":    {Type: PropertyTypeObject},
				"recipients": {Type: PropertyTypeArray},
				"count":      {Type: PropertyTypeInteger},
				"ratio":      {Type: PropertyTypeNumber},
				"enabled":    {Type: PropertyTypeBoolean},
			},
		}
		params := map[string]any{
			"arguments":  "{{ Configs['dev-pg'] }}",
			"headers":    "{{ Tasks['prev'].output.headers }}",
			"recipients": "{% if oncall %}{{ oncall.emails }}{% else %}{{ Inputs.fallback }}{% endif %}",
			"count":      "{{ Inputs.count }}",
			"ratio":      "{{ Inputs.ratio }}",
			"enabled":    "{{ Inputs.enabled }}",
		}
		err := templateSchema.Validate(params)
		assert.NoError(t, err)
	})

	t.Run("should reject non-template strings for non-string typed params", func(t *testing.T) {
		objSchema := &Schema{Properties: map[string]Property{"arguments": {Type: PropertyTypeObject}}}
		params := map[string]any{"arguments": "not a template, not a map"}
		err := objSchema.Validate(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected object (map)")
	})

	t.Run("should still reject wrong type on string-typed params even if string looks like template", func(t *testing.T) {
		strSchema := &Schema{Properties: map[string]Property{"url": {Type: PropertyTypeString}}}
		// Strings matching the template pattern are still valid for string fields —
		// they resolve to a string at runtime. Here the param is an int, which is
		// always invalid for a string field.
		params := map[string]any{"url": 42}
		err := strSchema.Validate(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid type for parameter 'url'")
	})
}
