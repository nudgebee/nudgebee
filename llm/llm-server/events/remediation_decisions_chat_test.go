package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
)

// A decision from an alert's chat is bound to the approval it was given under, so it needs no plan;
// every other decision still does.
func TestChatDecisionPlanIsOptional(t *testing.T) {
	chat := RemediationDecision{Data: map[string]any{"source": DecisionSourceChat}}
	assert.True(t, chat.planOptional())
	chat.PlanRowId, chat.PlanVersion = "row", "v1"
	assert.False(t, chat.planOptional(), "a chat decision on a plan step is checked against that plan")
	assert.False(t, RemediationDecision{Data: map[string]any{"source": "slack"}}.planOptional())
	assert.False(t, RemediationDecision{}.planOptional())

	repo := &EventAnalysisRepository{}
	_, _, err := repo.ClaimRemediationDecision(security.NewRequestContextForSuperAdmin(), RemediationDecision{AccountId: "a", EventId: "e", ApprovalKey: "k"})
	require.Error(t, err, "a panel decision without a plan is refused before touching the database")
}

// A plan step approved in the alert's chat is keyed by its approval: the same approval twice records
// one run, and the step, once decided under one key, is decided for every surface.
func TestClaimChatStepUnderItsApproval(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	plan := f.plans[event]
	stepKey := ApprovalKey(f.account, event, plan.version, "0:apply", "kubectl rollout undo deploy/api")
	claim := func(approvalKey, stepKey string) (string, *DecisionConflict) {
		id, conflict, err := f.repo.ClaimRemediationDecision(f.ctx, RemediationDecision{
			AccountId: f.account, EventId: event, UserId: "person", Ref: "kubectl rollout undo deploy/api",
			ApprovalKey: approvalKey, StepKey: stepKey, Status: RunStatusInProgress,
			Data:      map[string]any{"source": DecisionSourceChat, "step": "0:apply", "option": 1, "approved_by": "person"},
			PlanRowId: plan.id, PlanVersion: plan.version,
		})
		require.NoError(t, err)
		return id, conflict
	}
	id, conflict := claim("run_step:pv:1:apply:followup-1", stepKey)
	require.Nil(t, conflict)
	var stored struct {
		ApprovalKey string `db:"approval_key"`
		StepKey     string `db:"step_key"`
	}
	require.NoError(t, f.db.Get(&stored, `SELECT data->>'approval_key' AS approval_key, data->>'step_key' AS step_key FROM event_resolution WHERE id = $1`, id))
	assert.Equal(t, "run_step:pv:1:apply:followup-1", stored.ApprovalKey)
	assert.Equal(t, stepKey, stored.StepKey)

	_, conflict = claim("run_step:pv:1:apply:followup-1", stepKey)
	require.NotNil(t, conflict, "the same approval twice is one run")
	assert.Equal(t, DecisionAlreadyDecided, conflict.Reason)

	_, conflict = claim("run_step:pv:1:apply:followup-2", stepKey)
	require.NotNil(t, conflict, "another approval of a decided step")
	assert.Equal(t, DecisionAlreadyDecided, conflict.Reason)

	_, conflict = claim(stepKey, "")
	require.NotNil(t, conflict, "the panel's Run of a step decided in chat")
	assert.Equal(t, DecisionAlreadyDecided, conflict.Reason)
	var n int
	require.NoError(t, f.db.Get(&n, `SELECT count(*) FROM event_resolution WHERE event_id = $1`, event))
	assert.Equal(t, 1, n)
}

// A chat run is claimed without a plan and refuses a second run under the same approval; another
// command under that approval is refused with the command that used it.
func TestClaimChatDecisionWithoutPlan(t *testing.T) {
	f := newDecisionFixture(t)
	event := f.event()
	claim := func(command string) (string, *DecisionConflict) {
		id, conflict, err := f.repo.ClaimRemediationDecision(f.ctx, RemediationDecision{
			AccountId: f.account, EventId: event, UserId: "person", Ref: command, ApprovalKey: "kubectl_execute:followup-1",
			Status: RunStatusInProgress, Data: map[string]any{"source": DecisionSourceChat, "tool": "kubectl", "command": command},
		})
		require.NoError(t, err)
		return id, conflict
	}
	id, conflict := claim("kubectl rollout undo deploy/api")
	require.Nil(t, conflict)
	require.NotEmpty(t, id)
	settled, err := f.repo.SettleRemediationRun(f.ctx, id, RunStatusSuccess, map[string]any{"exit_code": 0, "exit_code_reported": true}, "ran")
	require.NoError(t, err)
	require.True(t, settled)

	_, conflict = claim("kubectl rollout undo deploy/api")
	require.NotNil(t, conflict)
	assert.Equal(t, DecisionAlreadyDecided, conflict.Reason)
	assert.Equal(t, "kubectl rollout undo deploy/api", conflict.Ref)

	_, conflict = claim("kubectl delete pod api-1")
	require.NotNil(t, conflict)
	assert.Equal(t, DecisionAlreadyDecided, conflict.Reason)
	assert.Equal(t, "kubectl rollout undo deploy/api", conflict.Ref, "the approval was used by another command")
}
