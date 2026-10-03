package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateFilter(t *testing.T) {
	payload := map[string]any{
		"event_type": "KubePodCrashLooping",
		"cluster":    "prod",
		"priority":   "P1",
	}

	tests := []struct {
		name      string
		filter    string
		want      bool
		wantError bool
	}{
		{name: "matching equality", filter: `{{ event.event_type == "KubePodCrashLooping" }}`, want: true},
		{name: "non-matching equality", filter: `{{ event.event_type == "Something else" }}`, want: false},
		{name: "and of two clauses", filter: `{{ event.priority == "P1" and event.cluster == "prod" }}`, want: true},
		{name: "and with one false clause", filter: `{{ event.priority == "P1" and event.cluster == "staging" }}`, want: false},
		{name: "in list", filter: `{{ event.cluster in ['prod', 'staging'] }}`, want: true},
		{name: "missing field is not a match", filter: `{{ event.nope == "x" }}`, want: false},
		{name: "literal 1 counts as true", filter: `1`, want: true},
		// A filter that blows up inside gonja must surface as an error, not take the
		// process down — the registry relies on this to skip one bad rule. (The custom
		// date filters like tz/strftime are registered by internal/workflow, so from
		// this package they are simply unknown; either way the failure is contained.)
		{name: "unresolvable filter returns an error", filter: `{{ now() | tz("Not/AZone") }}`, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, err := CompileFilter(tt.filter)
			require.NoError(t, err)

			matched, err := EvaluateFilter(query, payload)
			if tt.wantError {
				assert.Error(t, err)
				assert.False(t, matched)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, matched)
		})
	}
}

func TestEvaluateFilter_NilQueryMatchesEverything(t *testing.T) {
	matched, err := EvaluateFilter(nil, map[string]any{})
	assert.NoError(t, err)
	assert.True(t, matched)
}

func TestCompileFilter_RejectsBadSyntax(t *testing.T) {
	_, err := CompileFilter(`{{ event.event_type == }}`)
	assert.Error(t, err)
}
