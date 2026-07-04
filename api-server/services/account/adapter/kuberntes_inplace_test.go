package adapter

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResizePatchBody(t *testing.T) {
	containers := []any{
		map[string]any{
			"container_name": "app",
			"cpu_request":    "0.5",
			"cpu_limit":      nil, // omitted
			"memory_request": "663Mi",
			"memory_limit":   "663Mi",
		},
	}
	body, err := resizePatchBody(toResizeContainers(containers))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &parsed))
	specContainers := parsed["spec"].(map[string]any)["containers"].([]any)
	require.Len(t, specContainers, 1)
	c0 := specContainers[0].(map[string]any)
	assert.Equal(t, "app", c0["name"])
	res := c0["resources"].(map[string]any)
	reqs := res["requests"].(map[string]any)
	assert.Equal(t, "0.5", reqs["cpu"])
	assert.Equal(t, "663Mi", reqs["memory"])
	lims := res["limits"].(map[string]any)
	assert.Equal(t, "663Mi", lims["memory"])
	_, hasCPULimit := lims["cpu"]
	assert.False(t, hasCPULimit, "nil cpu_limit must be omitted, not set to <nil>")

	// No usable values → error.
	_, err = resizePatchBody(toResizeContainers([]any{map[string]any{"container_name": "x"}}))
	assert.Error(t, err)
}

func TestPodResizeState(t *testing.T) {
	mk := func(ctype, status, reason string) map[string]any {
		return map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": ctype, "status": status, "reason": reason},
				},
			},
		}
	}
	assert.Equal(t, "done", podResizeState(map[string]any{"status": map[string]any{}}))
	assert.Equal(t, "infeasible", podResizeState(mk("PodResizePending", "True", "Infeasible")))
	assert.Equal(t, "pending", podResizeState(mk("PodResizePending", "True", "Deferred")))
	assert.Equal(t, "inprogress", podResizeState(mk("PodResizeInProgress", "True", "")))
	assert.Equal(t, "infeasible", podResizeState(mk("PodResizeInProgress", "True", "Error")))
	// status False is ignored → done.
	assert.Equal(t, "done", podResizeState(mk("PodResizePending", "False", "Infeasible")))
}

func TestUnwrapKubectlOutput(t *testing.T) {
	// Top-level stdout.
	stdout, _ := unwrapKubectlOutput(map[string]any{"stdout": "hello"})
	assert.Equal(t, "hello", stdout)
	// JsonBlock under data.
	stdout, stderr := unwrapKubectlOutput(map[string]any{"data": `{"stdout":"out","stderr":"err"}`})
	assert.Equal(t, "out", stdout)
	assert.Equal(t, "err", stderr)
}
