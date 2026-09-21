package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// parseAccountFilter is the compatibility seam between the tenant-level
// Automations listing (which sends `account_ids`) and every pre-#35113 caller —
// including the `/automation?accountId=` deep links still scattered through the
// UI, which send a single `account_id`. Both must keep working, and "neither
// present" must mean "no filter" rather than an error.
func TestParseAccountFilter(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		expected []string
	}{
		{
			name:     "no account arguments means no filter",
			args:     map[string]any{},
			expected: nil,
		},
		{
			name:     "legacy single account_id",
			args:     map[string]any{"account_id": "acct-1"},
			expected: []string{"acct-1"},
		},
		{
			name:     "blank account_id is not a filter",
			args:     map[string]any{"account_id": ""},
			expected: nil,
		},
		{
			name:     "account_ids array",
			args:     map[string]any{"account_ids": []any{"acct-1", "acct-2"}},
			expected: []string{"acct-1", "acct-2"},
		},
		{
			name:     "account_ids wins over account_id",
			args:     map[string]any{"account_ids": []any{"acct-2"}, "account_id": "acct-1"},
			expected: []string{"acct-2"},
		},
		{
			name:     "empty account_ids falls back to account_id",
			args:     map[string]any{"account_ids": []any{}, "account_id": "acct-1"},
			expected: []string{"acct-1"},
		},
		{
			name:     "non-string entries are dropped",
			args:     map[string]any{"account_ids": []any{"acct-1", 42, "", nil}},
			expected: []string{"acct-1"},
		},
		{
			name:     "bare string account_ids is tolerated",
			args:     map[string]any{"account_ids": "acct-1"},
			expected: []string{"acct-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseAccountFilter(tt.args))
		})
	}
}

// mergeEventIntoInputs is the write-side counterpart to listExecutionsForEvent's
// read: it tags a manual trigger's inputs with the event it was run against so
// the run shows up in that event's automation history the same way a
// Nubi-triggered run already does.
func TestMergeEventIntoInputs(t *testing.T) {
	tests := []struct {
		name     string
		inputs   map[string]any
		eventID  string
		expected map[string]any
	}{
		{
			name:     "no event id leaves nil inputs nil",
			inputs:   nil,
			eventID:  "",
			expected: nil,
		},
		{
			name:     "no event id leaves existing inputs untouched",
			inputs:   map[string]any{"reason": "restart"},
			eventID:  "",
			expected: map[string]any{"reason": "restart"},
		},
		{
			name:     "event id tags nil inputs",
			inputs:   nil,
			eventID:  "evt-1",
			expected: map[string]any{"event": map[string]any{"id": "evt-1"}},
		},
		{
			name:     "event id tags existing inputs alongside other fields",
			inputs:   map[string]any{"reason": "restart"},
			eventID:  "evt-1",
			expected: map[string]any{"reason": "restart", "event": map[string]any{"id": "evt-1"}},
		},
		{
			name:     "a caller-supplied event input wins over the default",
			inputs:   map[string]any{"event": map[string]any{"id": "evt-caller-chosen"}},
			eventID:  "evt-from-page",
			expected: map[string]any{"event": map[string]any{"id": "evt-caller-chosen"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, mergeEventIntoInputs(tt.inputs, tt.eventID))
		})
	}
}
