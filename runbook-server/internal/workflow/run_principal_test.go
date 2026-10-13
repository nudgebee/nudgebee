package workflow

import (
	"context"
	"errors"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requestAs(userID string) *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("t1", userID, []string{"acct-a"})
}

func principalOf(t *testing.T, s *Service, ctx *security.RequestContext, wf *model.Workflow) string {
	t.Helper()
	p, err := s.runPrincipal(ctx, wf)
	require.NoError(t, err)
	return p
}

func TestRunPrincipalIsTheRequesterWhenThereIsOne(t *testing.T) {
	// Manual, retrigger and AI runs act as whoever started them, not as the
	// owner and not as the last editor.
	s := &Service{store: &MockWorkflowStore{runAs: map[string]string{"wf-1": "owner"}}}
	wf := &model.Workflow{ID: "wf-1", UpdatedBy: "last-editor", RunAsUserID: "owner"}
	assert.Equal(t, "requester", principalOf(t, s, requestAs("requester"), wf))
}

func TestRunPrincipalIsTheOwnerForSystemStartedRuns(t *testing.T) {
	// Event, webhook and optimization runs arrive with the system user.
	s := &Service{store: &MockWorkflowStore{runAs: map[string]string{"wf-1": "owner"}}}
	assert.Equal(t, "owner", principalOf(t, s, requestAs(security.GetSystemUserId()), &model.Workflow{ID: "wf-1"}))
	assert.Equal(t, "owner", principalOf(t, s, requestAs(""), &model.Workflow{ID: "wf-1"}))
}

func TestOwnerIsNeverTakenFromTheWorkflowBody(t *testing.T) {
	// Create/update bind the request body into model.Workflow and register its
	// schedule from it. A body naming an admin as run_as_user_id must not make
	// the schedule act as that admin.
	s := &Service{store: &MockWorkflowStore{runAs: map[string]string{"wf-1": "owner"}}}
	fromBody := &model.Workflow{ID: "wf-1", RunAsUserID: "tenant-admin"}
	owner, err := s.workflowOwner(context.Background(), "t1", fromBody)
	require.NoError(t, err)
	assert.Equal(t, "owner", owner)
	assert.Equal(t, "owner", principalOf(t, s, requestAs(security.GetSystemUserId()), fromBody))
}

func TestRunPrincipalIsEmptyWhenTheOwnerWasDeleted(t *testing.T) {
	// The FK sets run_as_user_id to NULL; every effectful step then refuses.
	s := &Service{store: &MockWorkflowStore{}}
	assert.Equal(t, "", principalOf(t, s, requestAs(security.GetSystemUserId()), &model.Workflow{ID: "wf-1", UpdatedBy: "last-editor"}))
}

func TestRecordOwnerMakesTheCallerTheOwner(t *testing.T) {
	store := &MockWorkflowStore{}
	s := &Service{store: store}
	wf := &model.Workflow{ID: "wf-1"}
	require.NoError(t, s.recordOwner(requestAs("publisher"), "acct-a", "wf-1", wf))
	owner, _ := store.GetRunAsUserID(context.Background(), "t1", "wf-1")
	assert.Equal(t, "publisher", owner)
	assert.Equal(t, "publisher", wf.RunAsUserID, "trigger registration that follows bakes the new owner")
}

func TestRecordOwnerLeavesTheOwnerAloneForSystemCallers(t *testing.T) {
	store := &MockWorkflowStore{runAs: map[string]string{"wf-1": "owner"}}
	s := &Service{store: store}
	require.NoError(t, s.recordOwner(requestAs(security.GetSystemUserId()), "acct-a", "wf-1", nil))
	owner, _ := store.GetRunAsUserID(context.Background(), "t1", "wf-1")
	assert.Equal(t, "owner", owner)
}

func TestRunPrincipalFailsTheRunStartWhenTheOwnerCannotBeRead(t *testing.T) {
	// A DB outage must not start a run that every step would then refuse as
	// "no owner".
	s := &Service{store: &MockWorkflowStore{runAsErr: errors.New("connection refused")}}
	_, err := s.runPrincipal(requestAs(security.GetSystemUserId()), &model.Workflow{ID: "wf-1"})
	assert.ErrorContains(t, err, "connection refused")
}
