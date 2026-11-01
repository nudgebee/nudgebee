package tickets

import (
	"nudgebee/runbook/internal/tasks/testutils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFoldLegacySeverity(t *testing.T) {
	t.Run("explicit severity wins over additional_fields", func(t *testing.T) {
		got := foldLegacySeverity(map[string]any{"priority": "High"}, "Low")
		assert.Equal(t, "Low", got)
	})

	t.Run("falls back to legacy priority when severity is empty", func(t *testing.T) {
		got := foldLegacySeverity(map[string]any{"priority": "High"}, "")
		assert.Equal(t, "High", got)
	})

	t.Run("does not fold urgency in: ServiceNow's Impact/Urgency are not a severity alias (#39810)", func(t *testing.T) {
		got := foldLegacySeverity(map[string]any{"impact": "Medium", "urgency": "Medium"}, "")
		assert.Equal(t, "", got)
	})

	t.Run("nil additional_fields", func(t *testing.T) {
		assert.Equal(t, "", foldLegacySeverity(nil, ""))
	})
}

func TestResolveReferenceID(t *testing.T) {
	cases := []struct {
		name     string
		ctx      *testutils.MockTaskContext
		expected string
	}{
		{
			name:     "event-triggered run keys on the event",
			ctx:      &testutils.MockTaskContext{EventID: "evt-1", WfID: "wf-1", WorkflowRunId: "run-1"},
			expected: "evt-1",
		},
		{
			name:     "manual or scheduled run keys on the run",
			ctx:      &testutils.MockTaskContext{WfID: "wf-1", WorkflowRunId: "run-1"},
			expected: "wf-1:run-1",
		},
		{
			name:     "run id alone keys on the run",
			ctx:      &testutils.MockTaskContext{WorkflowRunId: "run-1"},
			expected: "run-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, resolveReferenceID(tc.ctx))
		})
	}

	t.Run("isolated run task gets a unique reference", func(t *testing.T) {
		first := resolveReferenceID(&testutils.MockTaskContext{})
		second := resolveReferenceID(&testutils.MockTaskContext{})

		assert.True(t, strings.HasPrefix(first, "runtask:"))
		assert.NotEqual(t, first, second)
	})
}
