package k8s

import (
	"strings"
	"testing"

	"nudgebee/runbook/internal/tasks/testutils"

	"github.com/stretchr/testify/assert"
)

// getWorkflowBaseLink must not fabricate a workflow link for isolated "Run Task"
// executions, which carry no workflow id / run id. A malformed
// "/workflow/?accountId=...&executionId=" link would otherwise leak into GitOps
// PR bodies and Kubernetes annotations. Empty id => empty link so the callers'
// `if link != ""` guards actually skip it.
func TestGetWorkflowBaseLink_EmptyWorkflowIDReturnsEmpty(t *testing.T) {
	taskCtx := &testutils.MockTaskContext{
		Account:       "acct-1",
		WfID:          "", // isolated run — no real workflow execution
		WorkflowRunId: "",
	}

	assert.Equal(t, "", getWorkflowBaseLink(taskCtx))
}

func TestGetWorkflowBaseLink_PopulatedWorkflowIDReturnsLink(t *testing.T) {
	taskCtx := &testutils.MockTaskContext{
		Account:       "acct-1",
		WfID:          "wf-1",
		WorkflowRunId: "run-1",
	}

	link := getWorkflowBaseLink(taskCtx)
	assert.NotEqual(t, "", link)
	assert.True(t, strings.Contains(link, "/workflow/wf-1"), "link should embed the workflow id: %s", link)
	assert.True(t, strings.Contains(link, "accountId=acct-1"), "link should embed the account id: %s", link)
	assert.True(t, strings.Contains(link, "executionId=run-1"), "link should embed the run id: %s", link)
}
