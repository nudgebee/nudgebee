package workflow

import (
	"errors"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubFeatureFlag swaps the gate's feature-flag lookup for the duration of a
// test and restores it afterwards.
func stubFeatureFlag(t *testing.T, enabled bool, err error) {
	t.Helper()
	original := featureEnabledForAccount
	featureEnabledForAccount = func(_, _, _ string) (bool, error) { return enabled, err }
	t.Cleanup(func() { featureEnabledForAccount = original })
}

func aiRequestContext(aiTriggered bool) *security.RequestContext {
	ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	ctx.SetAITriggered(aiTriggered)
	return ctx
}

// invocableWorkflow is a workflow that passes every gate condition, so each
// subtest can knock out exactly one.
func invocableWorkflow() *model.Workflow {
	return &model.Workflow{
		ID:          "wf-1",
		Name:        "restart-payment-consumers",
		Status:      model.WorkflowStatusActive,
		AIInvocable: true,
		Definition: model.WorkflowDefinition{
			LLMDescription: "Restarts the payment consumers.",
			Triggers:       []model.Trigger{{Type: model.WorkflowTriggerManual}},
			Tasks:          []model.Task{{ID: "restart", Type: "run_script"}},
		},
	}
}

func TestAssertAIInvocationAllowed(t *testing.T) {
	s := &Service{}

	t.Run("allows a fully opted-in active workflow", func(t *testing.T) {
		stubFeatureFlag(t, true, nil)
		assert.NoError(t, s.assertAIInvocationAllowed(aiRequestContext(true), "test-account", invocableWorkflow()))
	})

	t.Run("denies when the feature flag is off", func(t *testing.T) {
		stubFeatureFlag(t, false, nil)
		err := s.assertAIInvocationAllowed(aiRequestContext(true), "test-account", invocableWorkflow())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not available to the AI assistant")
	})

	t.Run("denies when the flag lookup fails", func(t *testing.T) {
		// Fail closed: an unreachable metastore must not open the gate.
		stubFeatureFlag(t, true, errors.New("metastore unreachable"))
		assert.Error(t, s.assertAIInvocationAllowed(aiRequestContext(true), "test-account", invocableWorkflow()))
	})

	t.Run("denies a workflow that is not opted in", func(t *testing.T) {
		stubFeatureFlag(t, true, nil)
		wf := invocableWorkflow()
		wf.AIInvocable = false
		assert.Error(t, s.assertAIInvocationAllowed(aiRequestContext(true), "test-account", wf))
	})

	t.Run("denies a paused workflow even though humans may run it manually", func(t *testing.T) {
		stubFeatureFlag(t, true, nil)
		wf := invocableWorkflow()
		wf.Status = model.WorkflowStatusPaused
		assert.Error(t, s.assertAIInvocationAllowed(aiRequestContext(true), "test-account", wf))
	})

	t.Run("denies an inactive workflow", func(t *testing.T) {
		stubFeatureFlag(t, true, nil)
		wf := invocableWorkflow()
		wf.Status = model.WorkflowStatusInactive
		assert.Error(t, s.assertAIInvocationAllowed(aiRequestContext(true), "test-account", wf))
	})

	t.Run("denies when the live definition has no manual trigger", func(t *testing.T) {
		// The workflow row is opted in, but the version that would actually run
		// lost its manual trigger on a later publish.
		stubFeatureFlag(t, true, nil)
		wf := invocableWorkflow()
		wf.Definition.Triggers = []model.Trigger{{Type: model.WorkflowTriggerSchedule}}
		assert.Error(t, s.assertAIInvocationAllowed(aiRequestContext(true), "test-account", wf))
	})

	t.Run("denial message reveals nothing about why", func(t *testing.T) {
		// An AI-originated caller should not be able to probe workflow state by
		// reading back distinct refusal reasons.
		stubFeatureFlag(t, true, nil)
		notOptedIn := invocableWorkflow()
		notOptedIn.AIInvocable = false
		paused := invocableWorkflow()
		paused.Status = model.WorkflowStatusPaused

		errNotOptedIn := s.assertAIInvocationAllowed(aiRequestContext(true), "test-account", notOptedIn)
		errPaused := s.assertAIInvocationAllowed(aiRequestContext(true), "test-account", paused)
		require.Error(t, errNotOptedIn)
		require.Error(t, errPaused)
		assert.Equal(t, errNotOptedIn.Error(), errPaused.Error())
	})
}

func TestTriggerWorkflowFromDraftRejectsAI(t *testing.T) {
	// The draft path deliberately skips the live-version pointer, so allowing an
	// AI caller here would sidestep every published-definition check in the gate.
	// The refusal must come before the store is touched — hence the nil store.
	s := &Service{}

	_, err := s.TriggerWorkflowFromDraft(aiRequestContext(true), "test-account", "wf-1", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot run draft workflows")
}
