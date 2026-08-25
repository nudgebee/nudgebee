package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
)

// A run started by another workflow's Call Workflow step must be tagged as a run
// of the CALLEE — ListWorkflowExecutions filters on SearchAttrWorkflowID, so an
// untagged child never appears in the callee's Executions tab.
func TestApplyCalleeRunTags(t *testing.T) {
	versionName := "hotfix"
	version := &model.WorkflowVersion{ID: "version-id", VersionNumber: 4, Name: &versionName}

	searchAttrs := map[string]any{model.SearchAttrParentWorkflowID: "caller-id"}
	memo := map[string]any{"parent_task_id": "call_k8s"}

	applyCalleeRunTags(searchAttrs, memo, "callee-id", version)

	assert.Equal(t, "callee-id", searchAttrs[model.SearchAttrWorkflowID])
	assert.Equal(t, "called", searchAttrs[model.SearchAttrWorkflowTrigger])
	assert.Equal(t, "caller-id", searchAttrs[model.SearchAttrParentWorkflowID], "parent linkage must survive")

	assert.Equal(t, "version-id", memo[model.MemoWorkflowVersionID])
	assert.Equal(t, int64(4), memo[model.MemoWorkflowVersionNumber])
	assert.Equal(t, "call_k8s", memo["parent_task_id"], "existing memo keys must survive")
}

// Inline tasks that synthesize their own definition (core.group, core.foreach)
// have no callee, and must stay untagged so they don't pollute any workflow's
// execution list.
func TestApplyCalleeRunTagsNoCallee(t *testing.T) {
	searchAttrs := map[string]any{}
	memo := map[string]any{}

	applyCalleeRunTags(searchAttrs, memo, "", nil)

	assert.NotContains(t, searchAttrs, model.SearchAttrWorkflowID)
	assert.NotContains(t, searchAttrs, model.SearchAttrWorkflowTrigger)
	assert.Empty(t, memo)
}
