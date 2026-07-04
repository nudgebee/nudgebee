package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseKubeVersion(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
		wantErr      bool
	}{
		{"v1.33.2", 1, 33, false},
		{"v1.33.11-eks-40737a8", 1, 33, false},
		{"v1.35.3-gke.1389002", 1, 35, false},
		{"1.34.0+", 1, 34, false},
		{"  v1.32.0 ", 1, 32, false},
		{"not-a-version", 0, 0, true},
		{"", 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			v, err := parseKubeVersion(c.in)
			if c.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, c.major, v.major)
			assert.Equal(t, c.minor, v.minor)
		})
	}
}

func TestKubeVersionAtLeast(t *testing.T) {
	v := kubeVersion{major: 1, minor: 33}
	assert.True(t, v.atLeast(1, 33))
	assert.True(t, v.atLeast(1, 32))
	assert.False(t, v.atLeast(1, 34))
	assert.False(t, v.atLeast(1, 35))
	assert.True(t, v.atLeast(0, 99))
	assert.False(t, kubeVersion{major: 1, minor: 35}.atLeast(2, 0))
	assert.True(t, kubeVersion{major: 2, minor: 0}.atLeast(1, 99))
}

func TestIsInPlaceResizableKind(t *testing.T) {
	for _, k := range []string{"Deployment", "deployment", "StatefulSet", "ReplicaSet", "DaemonSet", "Pod"} {
		assert.True(t, isInPlaceResizableKind(k), k)
	}
	for _, k := range []string{"Job", "CronJob", "Rollout", "", "Service"} {
		assert.False(t, isInPlaceResizableKind(k), k)
	}
}

func TestResizeStatus(t *testing.T) {
	// No conditions → done.
	state, _ := resizeStatus(map[string]any{"status": map[string]any{}})
	assert.Equal(t, resizeDone, state)

	mkCond := func(ctype, status, reason string) map[string]any {
		return map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": ctype, "status": status, "reason": reason, "message": "msg"},
				},
			},
		}
	}

	state, _ = resizeStatus(mkCond("PodResizePending", "True", "Infeasible"))
	assert.Equal(t, resizeInfeasible, state)

	state, _ = resizeStatus(mkCond("PodResizePending", "True", "Deferred"))
	assert.Equal(t, resizeDeferred, state)

	state, _ = resizeStatus(mkCond("PodResizeInProgress", "True", ""))
	assert.Equal(t, resizeInProgress, state)

	state, _ = resizeStatus(mkCond("PodResizeInProgress", "True", "Error"))
	assert.Equal(t, resizeInfeasible, state)

	// Condition present but status False → ignored → done.
	state, _ = resizeStatus(mkCond("PodResizePending", "False", "Deferred"))
	assert.Equal(t, resizeDone, state)
}

func TestPodResourcesMatch(t *testing.T) {
	pod := map[string]any{
		"status": map[string]any{
			"containerStatuses": []any{
				map[string]any{
					"name": "app",
					"resources": map[string]any{
						"requests": map[string]any{"cpu": "500m", "memory": "512Mi"},
						"limits":   map[string]any{"memory": "512Mi"},
					},
				},
			},
		},
	}

	match := []map[string]any{
		{"name": "app", "resources": map[string]any{
			"requests": map[string]any{"cpu": "500m", "memory": "512Mi"},
			"limits":   map[string]any{"memory": "512Mi"},
		}},
	}
	assert.True(t, podResourcesMatch(pod, match))

	// 0.5 == 500m equivalently.
	equiv := []map[string]any{
		{"name": "app", "resources": map[string]any{"requests": map[string]any{"cpu": "0.5"}}},
	}
	assert.True(t, podResourcesMatch(pod, equiv))

	diff := []map[string]any{
		{"name": "app", "resources": map[string]any{"requests": map[string]any{"cpu": "750m"}}},
	}
	assert.False(t, podResourcesMatch(pod, diff))

	// Unknown container → no match.
	unknown := []map[string]any{
		{"name": "sidecar", "resources": map[string]any{"requests": map[string]any{"cpu": "500m"}}},
	}
	assert.False(t, podResourcesMatch(pod, unknown))
}
