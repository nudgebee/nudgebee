package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractEventIDFromInputs(t *testing.T) {
	cases := []struct {
		name     string
		inputs   map[string]any
		expected string
	}{
		{"no event input", map[string]any{"title": "x"}, ""},
		{"event not a map", map[string]any{"event": "raw"}, ""},
		{"id key", map[string]any{"event": map[string]any{"id": "evt-1"}}, "evt-1"},
		{"event_id key", map[string]any{"event": map[string]any{"event_id": "evt-2"}}, "evt-2"},
		{"id preferred over event_id", map[string]any{"event": map[string]any{"id": "evt-1", "event_id": "evt-2"}}, "evt-1"},
		{"empty id falls through to event_id", map[string]any{"event": map[string]any{"id": "", "event_id": "evt-2"}}, "evt-2"},
		{"non-string id ignored", map[string]any{"event": map[string]any{"id": 42}}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, extractEventIDFromInputs(tc.inputs))
		})
	}
}
