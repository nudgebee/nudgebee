package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"nudgebee/runbook/internal/model"
)

func TestForEachTask_GetLoopConfig(t *testing.T) {
	task := &ForEachTask{}

	tests := []struct {
		name          string
		params        map[string]any
		expectedItems []any
		expectedVar   string
		expectedBatch int
		expectedTasks int
		expectError   bool
	}{
		{
			name: "valid inputs",
			params: map[string]any{
				"items":       []any{"a", "b", "c"},
				"item":        "val",
				"tasks":       []model.Task{{ID: "t1", Type: "core.print"}},
				"concurrency": 2,
			},
			expectedItems: []any{"a", "b", "c"},
			expectedVar:   "val",
			expectedBatch: 2,
			expectedTasks: 1,
			expectError:   false,
		},
		{
			name: "default item var and concurrency",
			params: map[string]any{
				"items": []any{1, 2},
				"tasks": []model.Task{{ID: "t1", Type: "core.print"}},
			},
			expectedItems: []any{1, 2},
			expectedVar:   "item",
			expectedBatch: 1,
			expectedTasks: 1,
			expectError:   false,
		},
		{
			name: "items as json string",
			params: map[string]any{
				"items": `["x", "y"]`,
				"tasks": []model.Task{{ID: "t1", Type: "core.print"}},
			},
			expectedItems: []any{"x", "y"},
			expectedVar:   "item",
			expectedBatch: 1,
			expectedTasks: 1,
			expectError:   false,
		},
		{
			name: "missing items",
			params: map[string]any{
				"tasks": []model.Task{{ID: "t1", Type: "core.print"}},
			},
			expectError: true,
		},
		{
			name: "missing tasks",
			params: map[string]any{
				"items": []any{"a"},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock TaskContext (not used in GetLoopConfig currently, so can be nil or empty mock)
			// If needed we can create a simple mock
			config, err := task.GetLoopConfig(nil, tt.params)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedItems, config.Items)
				assert.Equal(t, tt.expectedVar, config.ItemVarName)
				assert.Equal(t, tt.expectedBatch, config.BatchSize)
				assert.Equal(t, tt.expectedTasks, len(config.Body.Tasks))
			}
		})
	}
}

func TestForEachTask_Schema(t *testing.T) {
	task := &ForEachTask{}
	assert.Equal(t, "core.foreach", task.GetName())
	assert.NotNil(t, task.InputSchema())
	assert.NotNil(t, task.OutputSchema())
}
