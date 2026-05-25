package core

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrintTask_Execute(t *testing.T) {
	task := &PrintTask{}
	logger := &TestLogger{Logger: slog.Default()}
	ctx := testutils.NewTestTaskContext("tenant", "account", "user", logger)

	tests := []struct {
		name        string
		params      map[string]any
		expectError bool
		expectData  string
	}{
		{
			name: "Valid Message",
			params: map[string]any{
				"message": "Hello World",
			},
			expectError: false,
			expectData:  "Hello World",
		},
		{
			name: "Missing Message",
			params: map[string]any{
				"other": "param",
			},
			expectError: true,
		},
		{
			name: "Invalid Message Type",
			params: map[string]any{
				"message": 123,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := task.Execute(ctx, tt.params)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				resMap, ok := res.(map[string]string)
				assert.True(t, ok)
				assert.Equal(t, tt.expectData, resMap["data"])
			}
		})
	}
}

func TestPrintTask_InputSchema(t *testing.T) {
	task := &PrintTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "message")
}

func TestPrintTask_OutputSchema(t *testing.T) {
	task := &PrintTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "data")
}
