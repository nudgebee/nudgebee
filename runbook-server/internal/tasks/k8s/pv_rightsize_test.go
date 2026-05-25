package k8s

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestApplyAbsoluteStorage(t *testing.T) {
	task := &PVRightsizeTask{}

	tests := []struct {
		name            string
		currentStorage  string
		changeTo        string
		expected        string
		expectedChanged bool
		expectErr       bool
	}{
		{
			name:            "Change to larger size",
			currentStorage:  "10Gi",
			changeTo:        "20Gi",
			expected:        "20Gi",
			expectedChanged: true,
			expectErr:       false,
		},
		{
			name:            "Change to smaller size (valid scenario, K8s might reject in practice)",
			currentStorage:  "10Gi",
			changeTo:        "5Gi",
			expected:        "5Gi",
			expectedChanged: true,
			expectErr:       false,
		},
		{
			name:            "No change",
			currentStorage:  "10Gi",
			changeTo:        "10Gi",
			expected:        "10Gi",
			expectedChanged: false,
			expectErr:       true, // Task skipped message
		},
		{
			name:            "Invalid changeTo format",
			currentStorage:  "10Gi",
			changeTo:        "abc",
			expected:        "",
			expectedChanged: false,
			expectErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentQ := resource.MustParse(tt.currentStorage)
			newQ, changed, err := task.applyAbsoluteStorage(currentQ, tt.changeTo)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, newQ.String())
			}
			assert.Equal(t, tt.expectedChanged, changed)
		})
	}
}

func TestApplyRateStorage(t *testing.T) {
	task := &PVRightsizeTask{}

	tests := []struct {
		name            string
		currentStorage  string
		changeBy        string
		expected        string
		expectedChanged bool
		expectErr       bool
	}{
		{
			name:            "Increase by 10%",
			currentStorage:  "10Gi",
			changeBy:        "10%",
			expected:        "11Gi",
			expectedChanged: true,
			expectErr:       false,
		},
		{
			name:            "Increase by 20 (as percentage)",
			currentStorage:  "10Gi",
			changeBy:        "20",
			expected:        "12Gi",
			expectedChanged: true,
			expectErr:       false,
		},
		{
			name:            "No actual change after calculation (0%)",
			currentStorage:  "10Gi",
			changeBy:        "0%",
			expected:        "10Gi",
			expectedChanged: false,
			expectErr:       true,
		},
		{
			name:            "Invalid changeBy format (not percentage)",
			currentStorage:  "10Gi",
			changeBy:        "1Gi", // This is not handled as a percentage
			expected:        "",
			expectedChanged: false,
			expectErr:       true,
		},
		{
			name:            "Invalid changeBy format (non-numeric)",
			currentStorage:  "10Gi",
			changeBy:        "abc%",
			expected:        "",
			expectedChanged: false,
			expectErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentQ := resource.MustParse(tt.currentStorage)
			newQ, changed, err := task.applyRateStorage(currentQ, tt.changeBy)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, newQ.String())
			}
			assert.Equal(t, tt.expectedChanged, changed)
		})
	}
}

func TestPVRightsizeTaskInputSchema(t *testing.T) {
	task := &PVRightsizeTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "namespace")
	assert.Contains(t, schema.Properties, "name")
	assert.Contains(t, schema.Properties, "kind")
	assert.Contains(t, schema.Properties, "change_by")
	assert.Contains(t, schema.Properties, "change_to")
	assert.Contains(t, schema.Properties, "max")
	assert.Contains(t, schema.Properties, "gitops_config") // New: Assert gitops_config is present

	gitopsConfigProp := schema.Properties["gitops_config"]
	assert.Equal(t, types.PropertyTypeObject, gitopsConfigProp.Type)
	assert.NotNil(t, gitopsConfigProp.Schema)
	assert.Contains(t, gitopsConfigProp.Schema.Properties, "enabled")

	enabledProp := gitopsConfigProp.Schema.Properties["enabled"]
	assert.Equal(t, types.PropertyTypeBoolean, enabledProp.Type)
	assert.Equal(t, false, enabledProp.Default)
}

func TestPVRightsizeTaskOutputSchema(t *testing.T) {
	task := &PVRightsizeTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "status")
	assert.Contains(t, schema.Properties, "old_storage")
	assert.Contains(t, schema.Properties, "new_storage")
	assert.Contains(t, schema.Properties, "patch")
	assert.Contains(t, schema.Properties, "resolution_id")
}

func TestPVRightsizeTask_Execute_ConstraintsAndDryRun_Validation(t *testing.T) {
	task := &PVRightsizeTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_K8S_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	testCases := []struct {
		name          string
		params        map[string]any
		expectErr     bool
		expectedError string
	}{
		{
			name:          "Missing Namespace",
			params:        map[string]any{"name": "test-pvc", "kind": "PersistentVolumeClaim", "change_to": "1Gi"},
			expectErr:     true,
			expectedError: "namespace, name, and kind are required",
		},
		{
			name:          "Missing Name",
			params:        map[string]any{"namespace": "default", "kind": "PersistentVolumeClaim", "change_to": "1Gi"},
			expectErr:     true,
			expectedError: "namespace, name, and kind are required",
		},
		{
			name:          "Missing Kind",
			params:        map[string]any{"namespace": "default", "name": "test-pvc", "change_to": "1Gi"},
			expectErr:     true,
			expectedError: "namespace, name, and kind are required",
		},
		{
			name:          "Invalid Kind",
			params:        map[string]any{"namespace": "default", "name": "test-pvc", "kind": "Deployment", "change_to": "1Gi"},
			expectErr:     true,
			expectedError: "pv rightsizing task is only applicable for PersistentVolumeClaim",
		},
		{
			name:          "Both change_by and change_to provided",
			params:        map[string]any{"namespace": "default", "name": "test-pvc", "kind": "PersistentVolumeClaim", "change_by": "10%", "change_to": "1Gi"},
			expectErr:     true,
			expectedError: "cannot provide both 'change_by' and 'change_to'",
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
