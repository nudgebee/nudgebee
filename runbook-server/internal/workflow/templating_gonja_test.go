package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTemplateContext_Gonja(t *testing.T) {
	t.Run("should correctly template using gonja syntax", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)
		ctx.Inputs["name"] = "world"

		// Using Jinja2/Gonja syntax specifically (filters)
		templateString := "Hello {{ Inputs.name | upper }}!"
		renderedString, err := ctx.renderGonja(templateString)

		assert.NoError(t, err)
		assert.Equal(t, "Hello WORLD!", renderedString)
	})

	t.Run("should handle nested structures in gonja", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)
		ctx.Tasks["task1"] = map[string]any{
			"output": "success",
		}

		templateString := "Task status: {{ Tasks.task1.output }}"
		renderedString, err := ctx.renderGonja(templateString)

		assert.NoError(t, err)
		assert.Equal(t, "Task status: success", renderedString)
	})

	t.Run("should handle loops in gonja", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)
		ctx.Inputs["items"] = []string{"a", "b", "c"}

		templateString := "{% for item in Inputs.items %}{{ item }}{% endfor %}"
		renderedString, err := ctx.renderGonja(templateString)

		assert.NoError(t, err)
		assert.Equal(t, "abc", renderedString)
	})

	t.Run("should handle complex expressions in gonja", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)
		ctx.Vars["count"] = 5

		templateString := "{% if Vars.count > 3 %}high{% else %}low{% endif %}"
		renderedString, err := ctx.renderGonja(templateString)

		assert.NoError(t, err)
		assert.Equal(t, "high", renderedString)
	})
}
