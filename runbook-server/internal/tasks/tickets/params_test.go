package tickets

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractAssignee(t *testing.T) {
	cases := []struct {
		name     string
		input    map[string]any
		expected string
	}{
		{"nil map", nil, ""},
		{"missing key", map[string]any{"other": "value"}, ""},
		{"string accountId", map[string]any{"assignee": "5b10ac8d82e05b22cc7d4ef5"}, "5b10ac8d82e05b22cc7d4ef5"},
		{"string email", map[string]any{"assignee": "user@example.com"}, "user@example.com"},
		{"object with id", map[string]any{"assignee": map[string]any{"id": "acct-123"}}, "acct-123"},
		{"object with name fallback", map[string]any{"assignee": map[string]any{"name": "john.doe"}}, "john.doe"},
		{"object with id preferred over name", map[string]any{"assignee": map[string]any{"id": "acct-1", "name": "john"}}, "acct-1"},
		{"empty string returns empty", map[string]any{"assignee": ""}, ""},
		{"object with empty id returns empty", map[string]any{"assignee": map[string]any{"id": ""}}, ""},
		{"unsupported type returns empty", map[string]any{"assignee": 42}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, extractAssignee(tc.input))
		})
	}
}
