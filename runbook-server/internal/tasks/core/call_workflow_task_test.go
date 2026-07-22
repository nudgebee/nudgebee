package core

import (
	"context"
	"encoding/json"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/testutils"

	"github.com/stretchr/testify/assert"
)

// liveVersionStore returns a draft definition via FindByName and a distinct LIVE
// version via GetLiveWorkflowVersion, so a test can prove which one is used.
type liveVersionStore struct {
	*testutils.MockWorkflowStore
	draftDef model.WorkflowDefinition
	liveDef  model.WorkflowDefinition
}

func (s *liveVersionStore) FindByName(ctx context.Context, tenantID, accountID, name string) (*model.Workflow, error) {
	return &model.Workflow{ID: "child-id", Name: name, TenantID: tenantID, AccountID: accountID, Definition: s.draftDef}, nil
}

func (s *liveVersionStore) GetLiveWorkflowVersion(ctx context.Context, workflowID string) (*model.WorkflowVersion, error) {
	return &model.WorkflowVersion{ID: "child-live-v", WorkflowID: workflowID, VersionNumber: 2, IsLive: true, Definition: s.liveDef}, nil
}

// pinnedVersionStore serves a distinct definition per version number so a test can
// prove a pinned `workflow_version` resolves via GetWorkflowVersion, not the Live
// pointer. GetLiveWorkflowVersion returns a poisoned def: if the resolver ever falls
// back to Live when a pin was requested, the assertion on task IDs catches it.
type pinnedVersionStore struct {
	*testutils.MockWorkflowStore
	versionDefs map[int]model.WorkflowDefinition
	liveDef     model.WorkflowDefinition
	gotVersion  int // records the version_number GetWorkflowVersion was asked for
}

func (s *pinnedVersionStore) FindByName(ctx context.Context, tenantID, accountID, name string) (*model.Workflow, error) {
	return &model.Workflow{ID: "child-id", Name: name, TenantID: tenantID, AccountID: accountID}, nil
}

func (s *pinnedVersionStore) GetWorkflowVersion(ctx context.Context, workflowID string, versionNumber int) (*model.WorkflowVersion, error) {
	s.gotVersion = versionNumber
	def := s.versionDefs[versionNumber]
	return &model.WorkflowVersion{ID: "child-pinned-v", WorkflowID: workflowID, VersionNumber: versionNumber, Definition: def}, nil
}

func (s *pinnedVersionStore) GetLiveWorkflowVersion(ctx context.Context, workflowID string) (*model.WorkflowVersion, error) {
	return &model.WorkflowVersion{ID: "child-live-v", WorkflowID: workflowID, VersionNumber: 99, IsLive: true, Definition: s.liveDef}, nil
}

// TestCallWorkflowPinnedVersion proves a `workflow_version` config value pins the
// child to that specific historical version via GetWorkflowVersion (#282), instead
// of the floating Live pointer.
func TestCallWorkflowPinnedVersion(t *testing.T) {
	store := &pinnedVersionStore{
		MockWorkflowStore: &testutils.MockWorkflowStore{},
		versionDefs: map[int]model.WorkflowDefinition{
			1: {Tasks: []model.Task{{ID: "v1-task", Type: "scripting.run_script"}}},
		},
		liveDef: model.WorkflowDefinition{Tasks: []model.Task{{ID: "live-task", Type: "scripting.run_script"}}},
	}

	ctx := newTestContext().(*testutils.MockTaskContext)
	ctx.WfStore = store

	task := &CallWorkflowTask{}
	wfDef, err := task.GetChildWorkflowDefinition(ctx, map[string]any{
		"workflow_name":    "child",
		"workflow_version": float64(1), // JSON-decoded config numbers arrive as float64
	})
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Equal(t, 1, store.gotVersion, "resolver must request the pinned version number")
	assert.Len(t, wfDef.Tasks, 1)
	assert.Equal(t, "v1-task", wfDef.Tasks[0].ID, "child must run the pinned version, not Live")
}

// TestCallWorkflowDefaultsToLiveWhenVersionAbsent is the backwards-compat guard:
// an existing Call Workflow action with no `workflow_version` must still follow the
// callee's Live pointer (no migration needed) (#282).
func TestCallWorkflowDefaultsToLiveWhenVersionAbsent(t *testing.T) {
	store := &pinnedVersionStore{
		MockWorkflowStore: &testutils.MockWorkflowStore{},
		versionDefs:       map[int]model.WorkflowDefinition{},
		liveDef:           model.WorkflowDefinition{Tasks: []model.Task{{ID: "live-task", Type: "scripting.run_script"}}},
	}

	ctx := newTestContext().(*testutils.MockTaskContext)
	ctx.WfStore = store

	task := &CallWorkflowTask{}
	wfDef, err := task.GetChildWorkflowDefinition(ctx, map[string]any{"workflow_name": "child"})
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Equal(t, 0, store.gotVersion, "GetWorkflowVersion must not be called when no version is pinned")
	assert.Equal(t, "live-task", wfDef.Tasks[0].ID, "absent version must default to Live")
}

// nilVersionStore returns (nil, nil) from GetWorkflowVersion — the not-found-but-no-error
// shape a store can produce. It proves resolveTargetVersion surfaces a clear error
// instead of letting the caller dereference a nil version (.Definition) and panic.
type nilVersionStore struct {
	*testutils.MockWorkflowStore
}

func (s *nilVersionStore) FindByName(ctx context.Context, tenantID, accountID, name string) (*model.Workflow, error) {
	return &model.Workflow{ID: "child-id", Name: name, TenantID: tenantID, AccountID: accountID}, nil
}

func (s *nilVersionStore) GetWorkflowVersion(ctx context.Context, workflowID string, versionNumber int) (*model.WorkflowVersion, error) {
	return nil, nil
}

// TestCallWorkflowPinnedVersionNotFound proves that when the store returns a nil
// version with no error, the resolver fails cleanly rather than panicking on a
// nil dereference downstream.
func TestCallWorkflowPinnedVersionNotFound(t *testing.T) {
	store := &nilVersionStore{MockWorkflowStore: &testutils.MockWorkflowStore{}}

	ctx := newTestContext().(*testutils.MockTaskContext)
	ctx.WfStore = store

	task := &CallWorkflowTask{}
	wfDef, err := task.GetChildWorkflowDefinition(ctx, map[string]any{
		"workflow_name":    "child",
		"workflow_version": float64(7),
	})
	assert.Error(t, err)
	assert.Nil(t, wfDef)
	assert.Contains(t, err.Error(), "not found")
}

// missingWorkflowStore returns (nil, nil) from FindByName — the not-found-but-no-error
// shape a store can produce. It proves both executor paths surface a clear error
// instead of dereferencing a nil workflow (.ID) and panicking.
type missingWorkflowStore struct {
	*testutils.MockWorkflowStore
}

func (s *missingWorkflowStore) FindByName(ctx context.Context, tenantID, accountID, name string) (*model.Workflow, error) {
	return nil, nil
}

// TestCallWorkflowNotFound proves that when FindByName returns a nil workflow with
// no error, GetChildWorkflowDefinition fails cleanly rather than panicking on wf.ID.
func TestCallWorkflowNotFound(t *testing.T) {
	store := &missingWorkflowStore{MockWorkflowStore: &testutils.MockWorkflowStore{}}

	ctx := newTestContext().(*testutils.MockTaskContext)
	ctx.WfStore = store

	task := &CallWorkflowTask{}
	wfDef, err := task.GetChildWorkflowDefinition(ctx, map[string]any{
		"workflow_name": "child",
	})
	assert.Error(t, err)
	assert.Nil(t, wfDef)
	assert.Contains(t, err.Error(), "not found")
}

// TestParseWorkflowVersionParam covers the JSON coercion + guard rails: only a
// positive whole number pins; anything else falls back to Live.
func TestParseWorkflowVersionParam(t *testing.T) {
	cases := []struct {
		name   string
		raw    any
		wantN  int
		wantOK bool
	}{
		{"absent/nil", nil, 0, false},
		{"float64 positive", float64(3), 3, true},
		{"int positive", 5, 5, true},
		{"int64 positive", int64(7), 7, true},
		{"json.Number positive", json.Number("4"), 4, true},
		{"zero", float64(0), 0, false},
		{"negative", float64(-2), 0, false},
		{"fractional", float64(2.5), 0, false},
		{"string valid", "2", 2, true},
		{"string invalid", "abc", 0, false},
		{"string zero", "0", 0, false},
		{"string negative", "-2", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := parseWorkflowVersionParam(tc.raw)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantN, n)
		})
	}
}

// TestCallWorkflowUsesLiveVersion is the H2 regression guard: core.call-workflow
// must build the child from the callee's LIVE published version, not its draft
// (workflows.definition). A published parent calling an edited-but-unpublished
// child must still run the child's last published graph.
func TestCallWorkflowUsesLiveVersion(t *testing.T) {
	store := &liveVersionStore{
		MockWorkflowStore: &testutils.MockWorkflowStore{},
		draftDef: model.WorkflowDefinition{
			Tasks: []model.Task{{ID: "draft-task", Type: "scripting.run_script"}},
		},
		liveDef: model.WorkflowDefinition{
			Tasks: []model.Task{{ID: "live-task", Type: "scripting.run_script"}},
		},
	}

	ctx := newTestContext().(*testutils.MockTaskContext)
	ctx.WfStore = store

	task := &CallWorkflowTask{}
	wfDef, err := task.GetChildWorkflowDefinition(ctx, map[string]any{"workflow_name": "child"})
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Len(t, wfDef.Tasks, 1)
	assert.Equal(t, "live-task", wfDef.Tasks[0].ID, "child must run the callee's LIVE version, not its draft")
}
