package system

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVerticalRightsizeGenerateTask_InputSchema(t *testing.T) {
	task := &VerticalRightsizeGenerateTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "account_id")
	assert.Contains(t, schema.Properties, "namespace")
	assert.Contains(t, schema.Properties, "workload_names")
	assert.Contains(t, schema.Properties, "batch_by_namespace")
	assert.Contains(t, schema.Properties, "persist_recommendation")
	assert.NotContains(t, schema.Properties, "tenant_id", "tenant_id is resolved from request context, not exposed to the form")
	assert.True(t, schema.Properties["account_id"].Required)
	assert.True(t, schema.Properties["namespace"].Required)
}

func TestVerticalRightsizeGenerateTask_OutputSchema(t *testing.T) {
	task := &VerticalRightsizeGenerateTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "status")
	assert.Contains(t, schema.Properties, "database_stored")
	assert.Contains(t, schema.Properties, "recommendations_count")
}

func TestVerticalRightsizeGenerateTask_Execute_Validation(t *testing.T) {
	task := &VerticalRightsizeGenerateTask{}

	testCases := []struct {
		name          string
		taskCtx       types.TaskContext
		params        map[string]any
		expectedError string
	}{
		{
			name:          "Missing Account ID",
			taskCtx:       testutils.NewTestTaskContext("tenant-1", "", "", slog.Default()),
			params:        map[string]any{},
			expectedError: "account_id is required",
		},
		{
			name:          "Missing Tenant ID",
			taskCtx:       testutils.NewTestTaskContext("", "", "", slog.Default()),
			params:        map[string]any{"account_id": "acc-1"},
			expectedError: "tenant_id is required",
		},
		{
			name:          "Missing Namespace",
			taskCtx:       testutils.NewTestTaskContext("tenant-1", "", "", slog.Default()),
			params:        map[string]any{"account_id": "acc-1"},
			expectedError: "namespace is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := task.Execute(tc.taskCtx, tc.params)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedError)
		})
	}
}
