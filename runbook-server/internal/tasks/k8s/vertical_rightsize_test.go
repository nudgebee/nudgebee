package k8s

import (
	"nudgebee/runbook/internal/tasks/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVerticalRightsizeTaskInputSchema(t *testing.T) {
	task := &VerticalRightsizeTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "namespace")
	assert.Contains(t, schema.Properties, "name")
	assert.Contains(t, schema.Properties, "kind")
	assert.Contains(t, schema.Properties, "direction")
	assert.Contains(t, schema.Properties, "cpu")
	assert.Contains(t, schema.Properties, "memory")
	assert.Contains(t, schema.Properties, "gitops_config") // New: Assert gitops_config is present

	gitopsConfigProp := schema.Properties["gitops_config"]
	assert.Equal(t, types.PropertyTypeObject, gitopsConfigProp.Type)
	assert.NotNil(t, gitopsConfigProp.Schema)
	assert.Contains(t, gitopsConfigProp.Schema.Properties, "enabled")

	enabledProp := gitopsConfigProp.Schema.Properties["enabled"]
	assert.Equal(t, types.PropertyTypeBoolean, enabledProp.Type)
	assert.Equal(t, false, enabledProp.Default)
}

func TestVerticalRightsizeTaskOutputSchema(t *testing.T) {
	task := &VerticalRightsizeTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "status")
	assert.Contains(t, schema.Properties, "patch")
	assert.Contains(t, schema.Properties, "resolution_id")
}
