package tickets

import (
	"nudgebee/runbook/internal/tasks/testutils"
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
		{
			name:     "isolated run task has no reference",
			ctx:      &testutils.MockTaskContext{},
			expected: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, resolveReferenceID(tc.ctx))
		})
	}
}
