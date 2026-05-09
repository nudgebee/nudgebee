package k8s

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types" // Added missing import
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPodDeleteTask_InputSchema(t *testing.T) {
	task := &PodDeleteTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "namespace")
	assert.Contains(t, schema.Properties, "name")
	assert.Contains(t, schema.Properties, "kind")
	assert.Contains(t, schema.Properties, "target_pod_name")
	assert.Contains(t, schema.Properties, "force") // Assert force is present

	// Check that kind has default and is string
	kindProp := schema.Properties["kind"]
	assert.Equal(t, types.PropertyTypeString, kindProp.Type)
	assert.Equal(t, "Pod", kindProp.Default)
}

func TestPodDeleteTask_OutputSchema(t *testing.T) {
	task := &PodDeleteTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "status")
	assert.Contains(t, schema.Properties, "deleted_pod")
	assert.Contains(t, schema.Properties, "namespace")
}

// TestPodDeleteTask_Execute_Validation tests basic input validation without relay calls
func TestPodDeleteTask_Execute_Validation(t *testing.T) {
	task := &PodDeleteTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name:          "Missing Namespace",
			params:        map[string]any{"name": "test"},
			expectErr:     true,
			expectedError: "namespace and name are required",
		},
		{
			name:          "Missing Name",
			params:        map[string]any{"namespace": "default"},
			expectErr:     true,
			expectedError: "namespace and name are required",
		},
		{
			name:          "Kind defaults to Pod and pod not found",
			params:        map[string]any{"namespace": "default", "name": "nonexistent-pod"},
			expectErr:     true,
			expectedError: "pods \"nonexistent-pod\" not found", // Expect error from kubectl
		},
		{
			name:          "StatefulSet Kind",
			params:        map[string]any{"namespace": "default", "name": "sts-name", "kind": "StatefulSet"},
			expectErr:     true,
			expectedError: "deleting pods from StatefulSets directly is not recommended",
		},
		{
			name:          "Valid Pod Deletion with Force (expect kubectl error for non-existent pod)",
			params:        map[string]any{"namespace": "default", "name": "nonexistent-pod-force", "force": true},
			expectErr:     true,
			expectedError: "pods \"nonexistent-pod-force\" not found", // Expect error from kubectl as pod won't exist
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
