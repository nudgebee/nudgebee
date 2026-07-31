package tickets

import (
	"testing"

	"nudgebee/runbook/internal/tasks/types"

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

func TestExtractRequiredStringSlice(t *testing.T) {
	cases := []struct {
		name     string
		input    map[string]any
		expected []string
		wantErr  bool
	}{
		{"single string wrapped", map[string]any{"assignee": "alice"}, []string{"alice"}, false},
		{"string trimmed", map[string]any{"assignee": "  alice  "}, []string{"alice"}, false},
		{"[]any of strings", map[string]any{"assignee": []any{"alice", "bob"}}, []string{"alice", "bob"}, false},
		{"[]string", map[string]any{"assignee": []string{"alice", "bob"}}, []string{"alice", "bob"}, false},
		{"[]any trims and drops empties", map[string]any{"assignee": []any{" alice ", "", "  "}}, []string{"alice"}, false},
		{"missing key errors", map[string]any{"other": "x"}, nil, true},
		{"empty string errors", map[string]any{"assignee": ""}, nil, true},
		{"empty slice errors", map[string]any{"assignee": []any{}}, nil, true},
		{"whitespace-only slice errors", map[string]any{"assignee": []any{"  ", ""}}, nil, true},
		{"non-string element errors", map[string]any{"assignee": []any{"alice", 42}}, nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractRequiredStringSlice(tc.input, "assignee")
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// The incident-only tasks must advertise their supported platforms on the
// integration_id field so the workflow UI can filter the dropdown; without it
// users could pick GitHub/GitLab/Jira and only find out at execution time
// (nudgebee-enterprise#34946).
func TestIncidentTasksDeclareSupportedPlatforms(t *testing.T) {
	tasks := map[string]interface{ InputSchema() *types.Schema }{
		"acknowledge": &TicketsAcknowledgeTask{},
		"escalate":    &TicketsEscalateTask{},
		"resolve":     &TicketsResolveTask{},
	}

	for name, task := range tasks {
		t.Run(name, func(t *testing.T) {
			prop, ok := task.InputSchema().Properties["integration_id"]
			assert.True(t, ok, "integration_id must be present in the input schema")
			assert.Equal(t, types.PropertyTypeTicket, prop.Type)
			assert.Equal(t, incidentPlatforms, prop.SubTypes)
		})
	}
}
