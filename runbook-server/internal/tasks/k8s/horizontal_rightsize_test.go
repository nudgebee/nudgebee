package k8s

import (
	"nudgebee/runbook/internal/tasks/types" // Added missing import
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyAbsoluteReplica(t *testing.T) {
	task := &HorizontalRightsizeTask{}

	tests := []struct {
		name             string
		currentReplicas  int64
		changeTo         int64
		scaleUp          bool
		expectedReplicas int64
		expectedChanged  bool
		expectErr        bool
	}{
		{
			name:             "Scale Up - Increase Replicas",
			currentReplicas:  3,
			changeTo:         5,
			scaleUp:          true,
			expectedReplicas: 5,
			expectedChanged:  true,
			expectErr:        false,
		},
		{
			name:             "Scale Up - Already at target (should not change)",
			currentReplicas:  5,
			changeTo:         5,
			scaleUp:          true,
			expectedReplicas: 5,
			expectedChanged:  false,
			expectErr:        true, // Should return error indicating skipped
		},
		{
			name:             "Scale Up - Target lower than current (should error/skip)",
			currentReplicas:  5,
			changeTo:         3,
			scaleUp:          true,
			expectedReplicas: 5, // Should not change
			expectedChanged:  false,
			expectErr:        true,
		},
		{
			name:             "Scale Down - Decrease Replicas",
			currentReplicas:  5,
			changeTo:         3,
			scaleUp:          false,
			expectedReplicas: 3,
			expectedChanged:  true,
			expectErr:        false,
		},
		{
			name:             "Scale Down - Already at target (should not change)",
			currentReplicas:  3,
			changeTo:         3,
			scaleUp:          false,
			expectedReplicas: 3,
			expectedChanged:  false,
			expectErr:        true, // Should return error indicating skipped
		},
		{
			name:             "Scale Down - Target higher than current (should error/skip)",
			currentReplicas:  3,
			changeTo:         5,
			scaleUp:          false,
			expectedReplicas: 3, // Should not change
			expectedChanged:  false,
			expectErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newReplicas, changed, err := task.applyAbsoluteReplica(tt.currentReplicas, tt.changeTo, tt.scaleUp)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedReplicas, newReplicas)
			assert.Equal(t, tt.expectedChanged, changed)
		})
	}
}

func TestApplyRateReplica(t *testing.T) {
	task := &HorizontalRightsizeTask{}

	tests := []struct {
		name             string
		currentReplicas  int64
		changeBy         int64
		scaleUp          bool
		min              int64
		max              int64
		expectedReplicas int64
		expectedChanged  bool
		expectErr        bool
	}{
		{
			name:            "Scale Up by 2",
			currentReplicas: 3,
			changeBy:        2,
			scaleUp:         true,
			min:             0, max: 0,
			expectedReplicas: 5,
			expectedChanged:  true,
			expectErr:        false,
		},
		{
			name:            "Scale Down by 1",
			currentReplicas: 3,
			changeBy:        1,
			scaleUp:         false,
			min:             0, max: 0,
			expectedReplicas: 2,
			expectedChanged:  true,
			expectErr:        false,
		},
		{
			name:            "Scale Up - Hit Max",
			currentReplicas: 3,
			changeBy:        3,
			scaleUp:         true,
			min:             0, max: 5,
			expectedReplicas: 3, // should not change, but report error
			expectedChanged:  false,
			expectErr:        true,
		},
		{
			name:            "Scale Down - Hit Min",
			currentReplicas: 3,
			changeBy:        3,
			scaleUp:         false,
			min:             1, max: 0,
			expectedReplicas: 3, // should not change, but report error
			expectedChanged:  false,
			expectErr:        true,
		},
		{
			name:            "Scale Down to Zero",
			currentReplicas: 1,
			changeBy:        2,
			scaleUp:         false,
			min:             0, max: 0,
			expectedReplicas: 0,
			expectedChanged:  true,
			expectErr:        false,
		},
		{
			name:            "No actual change",
			currentReplicas: 5,
			changeBy:        0, // No change
			scaleUp:         true,
			min:             0, max: 0,
			expectedReplicas: 5,
			expectedChanged:  false,
			expectErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newReplicas, changed, err := task.applyRateReplica(tt.currentReplicas, tt.changeBy, tt.scaleUp, tt.min, tt.max)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedReplicas, newReplicas)
			assert.Equal(t, tt.expectedChanged, changed)
		})
	}
}

func TestIsValidHorizontalRightsizeKind(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		expected bool
	}{
		{"Deployment", "Deployment", true},
		{"deployment", "deployment", true},
		{"StatefulSet", "StatefulSet", true},
		{"ReplicaSet", "ReplicaSet", true},
		{"Rollout", "Rollout", true},
		{"Pod", "Pod", false},
		{"DaemonSet", "DaemonSet", false},
		{"Service", "Service", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isValidHorizontalRightsizeKind(tt.kind))
		})
	}
}

func TestHorizontalRightsizeTaskInputSchema(t *testing.T) {
	task := &HorizontalRightsizeTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "namespace")
	assert.Contains(t, schema.Properties, "name")
	assert.Contains(t, schema.Properties, "kind")
	assert.Contains(t, schema.Properties, "direction")
	assert.Contains(t, schema.Properties, "scaling_mode")
	assert.Contains(t, schema.Properties, "change_by")
	assert.Contains(t, schema.Properties, "change_to")

	// Verify scaling_mode controls change_by/change_to visibility
	scalingModeProp := schema.Properties["scaling_mode"]
	assert.Equal(t, types.PropertyTypeString, scalingModeProp.Type)
	assert.Equal(t, []string{"change_by", "change_to"}, scalingModeProp.Options)

	changeByProp := schema.Properties["change_by"]
	assert.NotNil(t, changeByProp.VisibleWhen)
	assert.Equal(t, "scaling_mode", changeByProp.VisibleWhen.Field)

	changeToProp := schema.Properties["change_to"]
	assert.NotNil(t, changeToProp.VisibleWhen)
	assert.Equal(t, "scaling_mode", changeToProp.VisibleWhen.Field)
	assert.Contains(t, schema.Properties, "min")
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

func TestHorizontalRightsizeTaskOutputSchema(t *testing.T) {
	task := &HorizontalRightsizeTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "status")
	assert.Contains(t, schema.Properties, "old_replicas")
	assert.Contains(t, schema.Properties, "new_replicas")
	assert.Contains(t, schema.Properties, "patch")
	assert.Contains(t, schema.Properties, "resolution_id")
}
