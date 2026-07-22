package lifecycle

import (
	"log/slog"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

func resetHooks() {
	mu.Lock()
	defer mu.Unlock()
	hooks = map[Phase][]namedHandler{}
}

func testCtx() *security.RequestContext {
	return security.NewRequestContextForTenantAdmin("t1", slog.Default(), nil, nil)
}

// PhaseInvestigationEnqueued is not workflow-eligible, so Emit runs only the
// in-process hooks (no MQ publish) — ideal for exercising the registry.

func TestEmitRunsHooksInRegistrationOrder(t *testing.T) {
	resetHooks()
	var order []string
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "first", func(_ *security.RequestContext, _ map[string]any) error {
		order = append(order, "first")
		return nil
	})
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "second", func(_ *security.RequestContext, _ map[string]any) error {
		order = append(order, "second")
		return nil
	})

	Emit(testCtx(), PhaseInvestigationEnqueued, map[string]any{}, nil)
	assert.Equal(t, []string{"first", "second"}, order)
}

func TestEmitRunsOnlyMatchingPhaseHooks(t *testing.T) {
	resetHooks()
	ran := map[string]bool{}
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "a", func(_ *security.RequestContext, _ map[string]any) error {
		ran["a"] = true
		return nil
	})
	RegisterLifecycleHook(PhaseInvestigationWaiting, "b", func(_ *security.RequestContext, _ map[string]any) error {
		ran["b"] = true
		return nil
	})

	Emit(testCtx(), PhaseInvestigationEnqueued, map[string]any{}, nil)
	assert.True(t, ran["a"], "phase A hook should run")
	assert.False(t, ran["b"], "phase B hook must not run for phase A")
}

func TestEmitMergesExtraAndStampsPhase(t *testing.T) {
	resetHooks()
	var seen map[string]any
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "capture", func(_ *security.RequestContext, ev map[string]any) error {
		seen = ev
		return nil
	})

	ev := map[string]any{"id": "e1"}
	Emit(testCtx(), PhaseInvestigationEnqueued, ev, map[string]any{"analysis_summary": "s"})

	assert.Equal(t, "s", seen["analysis_summary"], "extra merged into event")
	assert.Equal(t, string(PhaseInvestigationEnqueued), seen[LifecyclePhaseKey], "phase stamped on event")
}

func TestPublishToWorkflowsNilEventNoPanic(t *testing.T) {
	// A nil event map must be a logged no-op, never a panic.
	assert.NotPanics(t, func() {
		PublishToWorkflows(testCtx(), PhaseEventCreated, nil)
	})
}

func TestPublishToWorkflowsStampsPhase(t *testing.T) {
	// Workflow-eligible phase: PublishToWorkflows stamps lifecycle_phase before
	// the publish. The empty cloud_account_id makes publishToWorkflows' own guard
	// short-circuit before any MQ call, so the test needs no broker.
	ev := map[string]any{"id": "e1"}
	PublishToWorkflows(testCtx(), PhaseEventCreated, ev)
	assert.Equal(t, string(PhaseEventCreated), ev[LifecyclePhaseKey], "phase stamped on event")
}

func TestPublishToWorkflowsNonEligiblePhaseDoesNotPublish(t *testing.T) {
	// PhaseInvestigationEnqueued is not workflow-eligible, so even a fully-formed
	// event (cloud_account_id + id, which would otherwise pass the publish guards)
	// must not reach the publish path. The phase is still stamped. No broker is
	// contacted, proving the eligibility gate.
	ev := map[string]any{"id": "e1", "cloud_account_id": "a1"}
	assert.NotPanics(t, func() {
		PublishToWorkflows(testCtx(), PhaseInvestigationEnqueued, ev)
	})
	assert.Equal(t, string(PhaseInvestigationEnqueued), ev[LifecyclePhaseKey], "phase stamped even when not published")
}

func TestEmitHookErrorDoesNotStopOthers(t *testing.T) {
	resetHooks()
	var ran []string
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "boom", func(_ *security.RequestContext, _ map[string]any) error {
		ran = append(ran, "boom")
		return assert.AnError
	})
	RegisterLifecycleHook(PhaseInvestigationEnqueued, "after", func(_ *security.RequestContext, _ map[string]any) error {
		ran = append(ran, "after")
		return nil
	})

	Emit(testCtx(), PhaseInvestigationEnqueued, map[string]any{}, nil)
	assert.Equal(t, []string{"boom", "after"}, ran, "a hook error must not stop later hooks")
}
