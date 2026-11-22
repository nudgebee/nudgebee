package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func liveDefinition(llmDescription string, trigger model.WorkflowTrigger) *model.WorkflowDefinition {
	return &model.WorkflowDefinition{
		LLMDescription: llmDescription,
		Triggers:       []model.Trigger{{Type: trigger}},
	}
}

// One rule decides whether the assistant is offered an automation, for both
// ai-search and the admins' list, and it names what to fix first.
func TestAIOfferReason(t *testing.T) {
	good := liveDefinition("Restarts the consumers.", model.WorkflowTriggerManual)
	cases := []struct {
		name      string
		enabled   bool
		invocable bool
		status    model.WorkflowStatus
		live      *model.WorkflowDefinition
		want      string
	}{
		{"offered", true, true, model.WorkflowStatusActive, good, ""},
		{"feature off wins over everything", false, false, model.WorkflowStatusPaused, nil, aiNotEnabled},
		{"not opted in", true, false, model.WorkflowStatusActive, good, aiNotOptedIn},
		{"paused", true, true, model.WorkflowStatusPaused, good, aiNotActive},
		{"never published", true, true, model.WorkflowStatusActive, nil, aiNotPublished},
		{"live version not manual", true, true, model.WorkflowStatusActive, liveDefinition("Cleans up.", model.WorkflowTriggerSchedule), aiNoManualTrigger},
		{"live version undescribed, whatever the draft says", true, true, model.WorkflowStatusActive, liveDefinition("  ", model.WorkflowTriggerManual), aiNoLLMDescription},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, aiOfferReason(tc.enabled, tc.invocable, tc.status, tc.live))
		})
	}
}

func TestListAIToolSettingsJudgesTheLiveVersion(t *testing.T) {
	stubFeatureFlag(t, true, nil)
	store := new(MockWorkflowStore)
	store.On("ListAIToolCandidates", mock.Anything, "test-tenant", []string{"test-account"}).Return([]model.AIToolCandidate{
		{ID: "offered", AccountID: "test-account", Status: model.WorkflowStatusActive, AIInvocable: true, AIAutoApprove: true,
			Live: liveDefinition("Restarts the consumers.", model.WorkflowTriggerManual)},
		// Draft has a description (so it is listed), the published version does not.
		{ID: "stale-live", AccountID: "test-account", Status: model.WorkflowStatusActive, AIInvocable: true,
			Live: liveDefinition("", model.WorkflowTriggerManual)},
		{ID: "candidate", AccountID: "test-account", Status: model.WorkflowStatusActive,
			Live: liveDefinition("Collects logs.", model.WorkflowTriggerManual)},
	}, nil)
	s := &Service{store: store}

	got, err := s.ListAIToolSettings(aiRequestContext(false), []string{"test-account"})
	require.NoError(t, err)
	require.Len(t, got.Workflows, 3)
	assert.True(t, got.Workflows[0].Offered)
	assert.True(t, got.Workflows[0].AIAutoApprove)
	assert.False(t, got.Workflows[1].Offered)
	assert.Equal(t, aiNoLLMDescription, got.Workflows[1].NotOfferedReason)
	assert.Equal(t, aiNotOptedIn, got.Workflows[2].NotOfferedReason)
}

func TestUpdateAISettings(t *testing.T) {
	describedManual := func() *model.Workflow {
		wf := invocableWorkflow()
		wf.AIInvocable = false
		return wf
	}

	t.Run("saves both flags without touching the definition", func(t *testing.T) {
		stubFeatureFlag(t, true, nil)
		store := new(MockWorkflowStore)
		store.On("Find", mock.Anything, "test-tenant", "test-account", "wf-1").Return(describedManual(), nil)
		store.On("UpdateAISettings", mock.Anything, "test-tenant", "test-account", "wf-1", "test-user", true, true).Return(nil)
		store.On("GetLiveWorkflowVersion", mock.Anything, "wf-1").
			Return(&model.WorkflowVersion{Definition: *liveDefinition("Restarts.", model.WorkflowTriggerManual)}, nil)
		s := &Service{store: store}

		got, err := s.UpdateAISettings(aiRequestContext(false), "test-account", "wf-1", true, true)
		require.NoError(t, err)
		assert.True(t, got.Offered)
		store.AssertExpectations(t)
		store.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("the assistant cannot change its own grant", func(t *testing.T) {
		store := new(MockWorkflowStore)
		s := &Service{store: store}
		_, err := s.UpdateAISettings(aiRequestContext(true), "test-account", "wf-1", false, false)
		require.Error(t, err)
		store.AssertNotCalled(t, "UpdateAISettings", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("running without asking needs the grant", func(t *testing.T) {
		s := &Service{store: new(MockWorkflowStore)}
		_, err := s.UpdateAISettings(aiRequestContext(false), "test-account", "wf-1", false, true)
		require.Error(t, err)
	})

	t.Run("granting needs an AI description in the draft", func(t *testing.T) {
		wf := describedManual()
		wf.Definition.LLMDescription = ""
		store := new(MockWorkflowStore)
		store.On("Find", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(wf, nil)
		s := &Service{store: store}
		_, err := s.UpdateAISettings(aiRequestContext(false), "test-account", "wf-1", true, false)
		require.ErrorContains(t, err, "AI description")
	})

	t.Run("an agent's automation is refused", func(t *testing.T) {
		wf := describedManual()
		wf.Binding = &model.WorkflowBinding{}
		store := new(MockWorkflowStore)
		store.On("Find", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(wf, nil)
		s := &Service{store: store}
		_, err := s.UpdateAISettings(aiRequestContext(false), "test-account", "wf-1", true, false)
		require.Error(t, err)
	})

	t.Run("saving what is stored writes nothing", func(t *testing.T) {
		stubFeatureFlag(t, true, nil)
		store := new(MockWorkflowStore)
		store.On("Find", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(invocableWorkflow(), nil)
		s := &Service{store: store}
		_, err := s.UpdateAISettings(aiRequestContext(false), "test-account", "wf-1", true, false)
		require.NoError(t, err)
		store.AssertNotCalled(t, "UpdateAISettings", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a request with no user is refused", func(t *testing.T) {
		ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "", []string{"test-account"})
		s := &Service{store: new(MockWorkflowStore)}
		_, err := s.UpdateAISettings(ctx, "test-account", "wf-1", false, false)
		require.Error(t, err)
	})

	t.Run("an account the caller cannot edit is refused", func(t *testing.T) {
		s := &Service{store: new(MockWorkflowStore)}
		_, err := s.UpdateAISettings(aiRequestContext(false), "other-account", "wf-1", false, false)
		require.Error(t, err)
	})
}
