package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyMemoryUnit(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"bytes float", float64(524288000), "500Mi"},
		{"bytes int", 1073741824, "1.0Gi"},
		// Regression for nudgebee-enterprise#33542: the AutoOptimize path carries
		// memory as an already-suffixed Ki string; it must be re-rendered compactly
		// instead of leaking "338077Ki" into the generated PR / values file.
		{"ki string", "338077Ki", "331Mi"},
		{"mi string passthrough-equivalent", "500Mi", "500Mi"},
		// Regression for the event-screen "Update" path: the UI sends memory as a
		// unit-suffixed MiB string; a fractional value must round up to a whole Mi
		// quantity. Sent as the bare number 160.79 instead, Kubernetes reads it as
		// bytes and renders "160790m" (~160 bytes), which OOM-kills the pod.
		{"fractional mi string (UI update path)", "160.79Mi", "161Mi"},
		{"gi string", "1.5Gi", "1.5Gi"},
		{"plain byte string", "346190848", "331Mi"},
		// CPU quantities and other non-memory strings must pass through untouched.
		{"cpu millis", "91m", "91m"},
		{"unparseable", "unset", "unset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, applyMemoryUnit(tt.in))
		})
	}
}

func TestFormatRequestedValuesForPrompt_KiString(t *testing.T) {
	data := map[string]any{
		"notifications": map[string]any{
			"cpu": map[string]any{"request": "91m"},
			"memory": map[string]any{
				"limit":   "338077Ki",
				"request": "338077Ki",
			},
		},
	}
	out := formatRequestedValuesForPrompt(data)
	mem := out["notifications"].(map[string]any)["memory"].(map[string]any)
	assert.Equal(t, "331Mi", mem["request"])
	assert.Equal(t, "331Mi", mem["limit"])
	// CPU is left untouched.
	cpu := out["notifications"].(map[string]any)["cpu"].(map[string]any)
	assert.Equal(t, "91m", cpu["request"])
}
