package event

import "testing"

// The 'workflow' -> 'automation' rename (V706) took ingestion down because the
// migration retired the lookup value while producers still emitted it
// (inc-cbb58afb8f). This pins the compatibility shim that keeps those producers
// working.
func TestNormalizeEventSource(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{"retired workflow source maps to automation", "workflow", "automation"},
		{"canonical automation source is unchanged", "automation", "automation"},
		{"unrelated source passes through", "prometheus", "prometheus"},
		{"empty source passes through", "", ""},
		{"match is exact, not case-insensitive", "Workflow", "Workflow"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeEventSource(tc.input); got != tc.want {
				t.Errorf("normalizeEventSource(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
