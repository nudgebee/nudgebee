package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCpuQuantityToCores(t *testing.T) {
	tests := []struct {
		name  string
		in    any
		want  float64
		wantK bool
	}{
		{"cores float", 0.5, 0.5, true},
		{"cores string", "2", 2, true},
		{"millis", "500m", 0.5, true},
		{"tiny millis", "2m", 0.002, true},
		{"tiny cores (event card)", 0.0017, 0.0017, true},
		{"nil", nil, 0, false},
		{"empty", "", 0, false},
		{"garbage", "abc", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := cpuQuantityToCores(tt.in)
			assert.Equal(t, tt.wantK, ok)
			if tt.wantK {
				assert.InDelta(t, tt.want, got, 1e-9)
			}
		})
	}
}

func container(fields map[string]any) []any {
	m := map[string]any{"container_name": "app"}
	for k, v := range fields {
		m[k] = v
	}
	return []any{m}
}

func TestValidateContainerResources(t *testing.T) {
	// Healthy applies pass, including a legitimately tiny CPU request (2m) — low
	// value is a recommendation decision, not a unit error, so it must not reject.
	require.NoError(t, validateContainerResources(container(map[string]any{
		"memory_request": "161Mi", "memory_limit": "161Mi", "cpu_request": "2m",
	})))
	require.NoError(t, validateContainerResources(container(map[string]any{
		"memory_request": "500Mi", "memory_limit": "750Mi", "cpu_request": "0.5", "cpu_limit": "0.8",
	})))
	// Unset fields (nil / empty) are skipped, not rejected.
	require.NoError(t, validateContainerResources(container(map[string]any{
		"memory_request": nil, "cpu_request": "",
	})))

	// The incident: the fix now yields "161Mi", but if a unit bug still slips a
	// byte-scale value through, the validator is the backstop that rejects it.
	assert.Error(t, validateContainerResources(container(map[string]any{"memory_request": "0Ki"})))
	assert.Error(t, validateContainerResources(container(map[string]any{"memory_request": float64(160.79)})))
	assert.Error(t, validateContainerResources(container(map[string]any{"memory_limit": "160790m"})))
	// millicores mistakenly applied as whole cores (500m -> 500 cores).
	assert.Error(t, validateContainerResources(container(map[string]any{"cpu_request": "500"})))
	// cpu must be strictly greater than 0 (K8s rejects a zero/negative request).
	assert.Error(t, validateContainerResources(container(map[string]any{"cpu_request": "0"})))
	assert.Error(t, validateContainerResources(container(map[string]any{"cpu_limit": "-1"})))
	// Absurd inflation.
	assert.Error(t, validateContainerResources(container(map[string]any{"memory_request": "5Ti"})))
}
