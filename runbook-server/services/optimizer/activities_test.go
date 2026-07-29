package optimizer

import (
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
)

// Only a task that applied the change itself resolves its recommendation.
// "resolved" is the status HandleGitOpsOrTicket returns when it hands the change
// to a recommendation resolution instead.
func TestClosesRecommendation(t *testing.T) {
	complete := string(model.AutopilotTaskStatusComplete)

	cases := []struct {
		name       string
		taskStatus string
		output     any
		want       bool
	}{
		{"direct apply", complete, map[string]any{"status": "success"}, true},
		{"ticket raised", complete, map[string]any{"status": "ticket_created"}, true},
		{"pvc issue raised", complete, map[string]any{"status": "issue_created"}, true},
		{"no status in output", complete, map[string]any{"patch": map[string]any{}}, true},
		{"output not a map", complete, "success", true},
		{"nil output", complete, nil, true},
		{"gitops pull request", complete, map[string]any{"status": "resolved"}, false},
		{"dry run", string(model.AutoOptimizeStatusDryrun), map[string]any{"status": "dry_run"}, false},
		{"skipped", string(model.AutopilotTaskStatusSkipped), map[string]any{"status": "skipped"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, closesRecommendation(tc.taskStatus, tc.output))
		})
	}
}
