package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/tools/core"
)

// TestNormaliseWorkflowUpdatePayload_FullWrapper — shape 2: caller already passed
// {name, definition: {...}}. The helper must keep it as-is, no fetch needed.
func TestNormaliseWorkflowUpdatePayload_FullWrapper(t *testing.T) {
	args := map[string]interface{}{
		"id": "wf-123",
		"definition": map[string]interface{}{
			"name": "ec2-reaper",
			"definition": map[string]interface{}{
				"version":  "v1",
				"tasks":    []interface{}{map[string]interface{}{"id": "t1", "type": "core.print"}},
				"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
			},
		},
	}

	out, err := NormaliseWorkflowUpdatePayload(core.NbToolContext{}, "wf-123", args, args["definition"])
	assert.NoError(t, err)

	payload, ok := out.(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "ec2-reaper", payload["name"])
	inner, ok := payload["definition"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "v1", inner["version"])
}

// TestNormaliseWorkflowUpdatePayload_FlatShape — shape 3: caller incorrectly put
// name + tasks + triggers all at the top level. The helper must rebuild as
// {name, definition: {tasks, triggers, version}}.
func TestNormaliseWorkflowUpdatePayload_FlatShape(t *testing.T) {
	flat := map[string]interface{}{
		"name":     "ec2-reaper",
		"version":  "v1",
		"tasks":    []interface{}{map[string]interface{}{"id": "t1", "type": "core.print"}},
		"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
	}
	args := map[string]interface{}{"id": "wf-123", "definition": flat}

	out, err := NormaliseWorkflowUpdatePayload(core.NbToolContext{}, "wf-123", args, flat)
	assert.NoError(t, err)

	payload, ok := out.(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "ec2-reaper", payload["name"])
	inner, ok := payload["definition"].(map[string]interface{})
	assert.True(t, ok)
	// "name" must NOT have leaked into the inner definition.
	_, hasName := inner["name"]
	assert.False(t, hasName, "name should be lifted to the wrapper, not duplicated inside definition")
	assert.Equal(t, "v1", inner["version"])
	assert.Len(t, inner["tasks"], 1)
	assert.Len(t, inner["triggers"], 1)
}

// TestNormaliseWorkflowUpdatePayload_InnerOnlyWithExplicitName — shape 1: caller
// passed only the inner def, but supplied the name via args["name"]. The helper
// must wrap with the explicit name, no fetch needed.
func TestNormaliseWorkflowUpdatePayload_InnerOnlyWithExplicitName(t *testing.T) {
	inner := map[string]interface{}{
		"version":  "v1",
		"tasks":    []interface{}{map[string]interface{}{"id": "t1", "type": "core.print"}},
		"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
	}
	args := map[string]interface{}{
		"id":         "wf-123",
		"name":       "ec2-reaper",
		"definition": inner,
	}

	out, err := NormaliseWorkflowUpdatePayload(core.NbToolContext{}, "wf-123", args, inner)
	assert.NoError(t, err)

	payload, ok := out.(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "ec2-reaper", payload["name"])
	wrappedInner, ok := payload["definition"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "v1", wrappedInner["version"])
}

// TestNormaliseWorkflowUpdatePayload_NonMapPassesThrough — pathological case where
// definition isn't a map. The helper should return it unchanged so the server can
// produce a sensible validation error.
func TestNormaliseWorkflowUpdatePayload_NonMapPassesThrough(t *testing.T) {
	args := map[string]interface{}{"id": "wf-123", "definition": "raw string"}
	out, err := NormaliseWorkflowUpdatePayload(core.NbToolContext{}, "wf-123", args, "raw string")
	assert.NoError(t, err)
	assert.Equal(t, "raw string", out)
}
