package adapter

import (
	"nudgebee/services/internal/annotations"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResizePolicyEntries(t *testing.T) {
	// Default (empty mode): cpu/memory both NotRequired.
	entries, inject := resizePolicyEntries("")
	require.True(t, inject)
	require.Len(t, entries, 2)
	assert.Equal(t, resizePolicyEntry{"cpu", "NotRequired"}, entries[0])
	assert.Equal(t, resizePolicyEntry{"memory", "NotRequired"}, entries[1])

	// restart-memory: memory becomes RestartContainer.
	entries, inject = resizePolicyEntries("restart-memory")
	require.True(t, inject)
	assert.Equal(t, "NotRequired", entries[0].RestartPolicy)
	assert.Equal(t, "RestartContainer", entries[1].RestartPolicy)

	// Disabled variants → no injection.
	for _, v := range []string{"false", "off", "disabled", "no", "FALSE", " Off "} {
		_, inject := resizePolicyEntries(v)
		assert.False(t, inject, "value %q should disable injection", v)
	}
}

func TestResolveResizePolicyMode(t *testing.T) {
	ann := map[string]string{annotations.CIInPlaceResize: "restart-memory"}
	// Explicit override wins over the annotation.
	assert.Equal(t, "disabled", resolveResizePolicyMode("disabled", ann))
	// Empty override falls back to the annotation.
	assert.Equal(t, "restart-memory", resolveResizePolicyMode("", ann))
	// Whitespace override is treated as empty.
	assert.Equal(t, "restart-memory", resolveResizePolicyMode("  ", ann))
	// No override, no annotation → empty.
	assert.Equal(t, "", resolveResizePolicyMode("", map[string]string{}))
}

func TestK8sSupportsInPlaceResize(t *testing.T) {
	// In-place resize is gated on >= 1.35 (GA), not the 1.33/1.34 beta.
	yes := []string{"v1.35.0", "v1.35.3-gke.1389002", "1.36.0+", "v2.0.1"}
	no := []string{"v1.34.9", "v1.33.11-eks-40737a8", "v1.32.0", "v1.27.0", "1.30.2", "", "garbage", "v1"}
	for _, v := range yes {
		assert.True(t, k8sSupportsInPlaceResize(v), "%q should be >= 1.35", v)
	}
	for _, v := range no {
		assert.False(t, k8sSupportsInPlaceResize(v), "%q should be < 1.35 / invalid", v)
	}
}

// TestResizePolicyPromptSection verifies the instruction block injected into
// the @agent_code_2 prompt: present with the right memory policy when enabled,
// empty when disabled.
func TestResizePolicyPromptSection(t *testing.T) {
	// Default → section present, both NotRequired.
	s := resizePolicyPromptSection("")
	assert.Contains(t, s, "resizePolicy")
	assert.Contains(t, s, "In-Place Resize")
	assert.Contains(t, s, "resourceName: memory\n       restartPolicy: NotRequired")

	// restart-memory → memory RestartContainer.
	s = resizePolicyPromptSection("restart-memory")
	assert.Contains(t, s, "resourceName: memory\n       restartPolicy: RestartContainer")

	// Disabled → empty section.
	s = resizePolicyPromptSection("false")
	assert.Equal(t, "", s)
	assert.False(t, strings.Contains(s, "resizePolicy"))
}
