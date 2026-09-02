package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseApprovedToolsParam(t *testing.T) {
	cases := []struct {
		name    string
		input   any
		want    map[string]string
		wantErr bool
	}{
		// Nothing approved is the norm, and must stay indistinguishable from
		// "not set" so every tool keeps prompting.
		{"nil", nil, nil, false},
		{"empty string", "", nil, false},
		{"empty list", []any{}, nil, false},
		{"blank entries only", []any{" ", ""}, nil, false},

		{"single string", "workflow_trigger", map[string]string{"workflow_trigger": "yes"}, false},
		{"comma separated", "workflow_trigger, tickets_v2",
			map[string]string{"workflow_trigger": "yes", "tickets_v2": "yes"}, false},
		{"typed list", []string{"workflow_trigger"}, map[string]string{"workflow_trigger": "yes"}, false},
		{"untyped list", []any{"workflow_trigger", "tickets_v2"},
			map[string]string{"workflow_trigger": "yes", "tickets_v2": "yes"}, false},
		{"blanks dropped", []any{"workflow_trigger", "  "},
			map[string]string{"workflow_trigger": "yes"}, false},

		// A malformed value must fail loudly. Silently dropping it would leave
		// the agent waiting on a confirmation nobody can answer.
		{"wrong type", 42, nil, true},
		{"wrong type in list", []any{"workflow_trigger", 42}, nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseApprovedToolsParam(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
