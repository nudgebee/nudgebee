package tools

import (
	"encoding/json"
	"strings"
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

func TestProjectWorkflowListResponse_TrimsVerboseFields(t *testing.T) {
	raw := []byte(`{
		"workflows": [{
			"id": "wf-1",
			"tenant_id": "t-1",
			"account_id": "a-1",
			"name": "db-monitor",
			"status": "ACTIVE",
			"last_execution_status": "FAILED",
			"last_execution_status_message": "boom",
			"last_execution_time": "2026-05-18T12:13:18Z",
			"created_by": "u-1",
			"created_by_user": {"id": "u-1", "display_name": "Alice"},
			"updated_by_user": {"id": "u-1", "display_name": "Alice"},
			"created_from_session_id": "s-1",
			"definition": {
				"version": "v1",
				"triggers": [{"type": "event", "params": {"filter": "..."}}],
				"tasks": [{"id": "t1", "type": "scripting.run_script", "params": {"script": "huge"}}]
			}
		}],
		"next_page_token": "1",
		"total_count": 170
	}`)

	got := projectWorkflowListResponse(raw)
	var parsed map[string]any
	assert.NoError(t, json.Unmarshal(got, &parsed))

	assert.Equal(t, "1", parsed["next_page_token"])
	assert.EqualValues(t, 170, parsed["total_count"])

	wfs := parsed["workflows"].([]any)
	assert.Len(t, wfs, 1)
	wf := wfs[0].(map[string]any)

	assert.Equal(t, "wf-1", wf["id"])
	assert.Equal(t, "db-monitor", wf["name"])
	assert.Equal(t, "ACTIVE", wf["status"])
	assert.Equal(t, "FAILED", wf["last_execution_status"])
	assert.Equal(t, "boom", wf["last_execution_status_message"])
	assert.Equal(t, "2026-05-18T12:13:18Z", wf["last_execution_time"])
	assert.Equal(t, []any{"event"}, wf["trigger_types"])

	for _, dropped := range []string{
		"tenant_id", "account_id", "created_by", "created_by_user",
		"updated_by_user", "created_from_session_id", "definition",
	} {
		_, present := wf[dropped]
		assert.False(t, present, "expected %q to be dropped", dropped)
	}
}

func TestProjectWorkflowListResponse_TruncatesLongStatusMessage(t *testing.T) {
	long := strings.Repeat("x", 500)
	raw, err := json.Marshal(map[string]any{
		"workflows": []map[string]any{{
			"id":                            "wf-1",
			"last_execution_status_message": long,
		}},
	})
	assert.NoError(t, err)

	var parsed map[string]any
	assert.NoError(t, json.Unmarshal(projectWorkflowListResponse(raw), &parsed))
	wf := parsed["workflows"].([]any)[0].(map[string]any)
	msg := wf["last_execution_status_message"].(string)
	assert.Equal(t, 203, len(msg)) // 200 runes + "..."
	assert.True(t, strings.HasSuffix(msg, "..."))
}

func TestProjectWorkflowListResponse_InvalidJSONPassesThrough(t *testing.T) {
	raw := []byte(`not json at all`)
	assert.Equal(t, raw, projectWorkflowListResponse(raw))
}

func TestProjectWorkflowListResponse_UnexpectedShapePassesThrough(t *testing.T) {
	// No "workflows" key — leave it alone.
	raw := []byte(`{"error": "unauthorized"}`)
	assert.Equal(t, raw, projectWorkflowListResponse(raw))
}
