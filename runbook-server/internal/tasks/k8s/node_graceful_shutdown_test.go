package k8s

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types" // Added missing import
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNodeGracefulShutdownTask_InputSchema(t *testing.T) {
	task := &NodeGracefulShutdownTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "name")
	assert.Contains(t, schema.Properties, "delete_node")
	assert.Contains(t, schema.Properties, "ignore_pdbs") // Assert ignore_pdbs is present

	// Check that ignore_pdbs has default and is boolean
	ignorePDBsProp := schema.Properties["ignore_pdbs"]
	assert.Equal(t, types.PropertyTypeBoolean, ignorePDBsProp.Type)
	assert.Equal(t, false, ignorePDBsProp.Default)
}

func TestNodeGracefulShutdownTask_OutputSchema(t *testing.T) {
	task := &NodeGracefulShutdownTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "status")
	assert.Contains(t, schema.Properties, "node")
	assert.Contains(t, schema.Properties, "cordoned")
	assert.Contains(t, schema.Properties, "drained")
	assert.Contains(t, schema.Properties, "deleted")
}

func TestNodeGracefulShutdownTask_Execute_Validation(t *testing.T) {
	task := &NodeGracefulShutdownTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name:          "Missing Node Name",
			params:        map[string]any{},
			expectErr:     true,
			expectedError: "node name is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := task.Execute(taskCtx, tc.params)
			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectedError != "" {
					assert.Contains(t, err.Error(), tc.expectedError)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
