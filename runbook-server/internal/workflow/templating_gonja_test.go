package workflow

import (
	"testing"
	"time"

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

func TestRenderGonjaUsesOneTimestampPerRender(t *testing.T) {
	ctx := NewTemplateContext(nil, nil)
	first := time.Date(2026, time.November, 5, 23, 59, 59, 0, time.UTC)
	clockCalls := 0
	ctx.timeSource = func() time.Time {
		clockCalls++
		return first.Add(time.Duration(clockCalls-1) * time.Minute)
	}

	rendered, err := ctx.Render(`{{ date }}|{{ date_iso }}|{{ date_us }}|{{ datetime }}|{{ timestamp_iso }}|{{ now() | strftime("%Y-%m-%dT%H:%M:%SZ") }}`)

	assert.NoError(t, err)
	assert.Equal(t, 1, clockCalls)
	assert.Equal(t, "05112026|2026-11-05|11/05/2026|05112026_2359|2026-11-05T23:59:59Z|2026-11-05T23:59:59Z", rendered)
}
