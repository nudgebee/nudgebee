package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// collectInvalidateNamespaces accepts the legacy single-namespace shape,
// the new list shape, or both together; trims, dedupes, and drops empties.
func TestCollectInvalidateNamespaces(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    []string
	}{
		{
			name:    "legacy_single",
			payload: map[string]any{"namespace": "llm_tool_config"},
			want:    []string{"llm_tool_config"},
		},
		{
			name:    "list_form",
			payload: map[string]any{"namespaces": []any{"a", "b"}},
			want:    []string{"a", "b"},
		},
		{
			name:    "merges_both_shapes",
			payload: map[string]any{"namespace": "a", "namespaces": []any{"b", "c"}},
			want:    []string{"a", "b", "c"},
		},
		{
			name:    "dedupes_and_trims",
			payload: map[string]any{"namespaces": []any{"a", " a ", "", "b", "a"}},
			want:    []string{"a", "b"},
		},
		{
			name:    "empty_payload",
			payload: map[string]any{},
			want:    []string{},
		},
		{
			name:    "ignores_non_string_entries",
			payload: map[string]any{"namespaces": []any{"a", 42, nil, "b"}},
			want:    []string{"a", "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collectInvalidateNamespaces(tc.payload)
			assert.ElementsMatch(t, tc.want, got)
		})
	}
}
