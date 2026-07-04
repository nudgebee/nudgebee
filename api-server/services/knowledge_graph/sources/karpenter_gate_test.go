package sources

import "testing"

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// TestShouldFetchKarpenterCRDs covers the gate policy: skip only on positive
// evidence of a non-Karpenter autoscaler; probe on anything unknown.
func TestShouldFetchKarpenterCRDs(t *testing.T) {
	tests := []struct {
		name          string
		autoscalerTyp *string
		enabled       *bool
		wantFetch     bool
	}{
		{"karpenter, enabled", strPtr("karpenter"), boolPtr(true), true},
		{"karpenter, enabled nil", strPtr("karpenter"), nil, true},
		{"karpenter, mixed case", strPtr("Karpenter"), nil, true},
		{"karpenter, explicitly disabled", strPtr("karpenter"), boolPtr(false), false},
		{"gke autoscaler", strPtr("gke"), boolPtr(true), false},
		{"cluster-autoscaler", strPtr("cluster-autoscaler"), boolPtr(true), false},
		{"type nil -> probe", nil, nil, true},
		{"type empty -> probe", strPtr(""), nil, true},
		{"type whitespace -> probe", strPtr("  "), nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := shouldFetchKarpenterCRDs(tc.autoscalerTyp, tc.enabled)
			if got != tc.wantFetch {
				t.Errorf("shouldFetchKarpenterCRDs() = %v (reason %q), want %v", got, reason, tc.wantFetch)
			}
		})
	}
}
