package tickets

import (
	"nudgebee/runbook/internal/tasks/testutils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
